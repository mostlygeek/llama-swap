#!/usr/bin/env python3
"""Fine-tunes a model on recorded Docs Agent conversations with LoRA.

Each assistant turn is one example: the conversation so far is context, and only
that turn is trained on. History renders as it does in production, where the
client never sends reasoning back, so only the turn being predicted has reasoning.

    PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True \
    CUDA_DEVICE_ORDER=PCI_BUS_ID CUDA_VISIBLE_DEVICES=3 \
    ~/lfm3/train/.venv/bin/python evals/docs-agent/training/train.py \
        --model ~/lfm3/base-hf --data evals/docs-agent/training/data \
        --out ~/lfm3/runs/lfm3-v2
"""

import argparse
import json
import pathlib
import shutil

import zstandard

TARGET_MODULES = "q_proj k_proj v_proj o_proj in_proj out_proj w1 w2 w3".split()


def template_messages(messages):
    """Decodes tool arguments, which the template requires as mappings."""
    out = []
    for message in messages:
        message = dict(message)
        if message.get("tool_calls"):
            calls = []
            for call in message["tool_calls"]:
                fn = dict(call["function"])
                fn["arguments"] = json.loads(fn["arguments"] or "{}")
                calls.append({**call, "function": fn})
            message["tool_calls"] = calls
        out.append(message)
    return out


def template_tools(tools):
    """Tools as llama-server renders them: it drops everything but these fields."""
    keep = ("name", "description", "parameters")
    return [
        {**tool, "function": {k: tool["function"][k] for k in keep if k in tool["function"]}}
        for tool in tools
    ]


def history(messages):
    """Earlier turns as the client sends them, with no reasoning."""
    return [
        {k: v for k, v in m.items() if k not in ("reasoning_content", "skip")}
        for m in messages
    ]


def render_examples(record, tools, render):
    """Yields (prompt, completion) text for each assistant turn."""
    messages = template_messages(record["messages"])
    for index, message in enumerate(messages):
        if message["role"] != "assistant" or message.get("skip"):
            continue
        context = history(messages[:index])
        prompt = render(context, tools, True)
        full = render(context + [message], tools, False)
        if not full.startswith(prompt):
            raise ValueError(f"{record['id']}: prompt is not a prefix of the turn")
        yield prompt, full[len(prompt) :]


def load_examples(path, tools, tokenizer, max_length):
    def render(messages, tools, generation):
        return tokenizer.apply_chat_template(
            messages,
            tools=tools,
            add_generation_prompt=generation,
            tokenize=False,
        )

    examples = []
    skipped = 0
    for line in zstandard.open(path, "rt", encoding="utf-8"):
        record = json.loads(line)
        for prompt, completion in render_examples(record, tools, render):
            encode = lambda text: tokenizer(text, add_special_tokens=False)["input_ids"]
            prompt_ids, completion_ids = encode(prompt), encode(completion)
            if len(prompt_ids) + len(completion_ids) > max_length:
                skipped += 1
                continue
            examples.append(
                {
                    "input_ids": prompt_ids + completion_ids,
                    "labels": [-100] * len(prompt_ids) + completion_ids,
                }
            )
    return examples, skipped


class Collator:
    def __init__(self, pad_id, torch):
        self.pad_id = pad_id
        self.torch = torch

    def __call__(self, batch):
        width = max(len(item["input_ids"]) for item in batch)
        padded = lambda ids, fill: ids + [fill] * (width - len(ids))
        tensor = self.torch.tensor
        return {
            "input_ids": tensor([padded(b["input_ids"], self.pad_id) for b in batch]),
            "labels": tensor([padded(b["labels"], -100) for b in batch]),
            "attention_mask": tensor(
                [padded([1] * len(b["input_ids"]), 0) for b in batch]
            ),
        }


def completion_loss(model, inputs, cross_entropy, tokens=None):
    """Loss per target token, computing logits only from the first of them.

    Full logits for a 13,000 token prompt take 7 GB, and nearly all are masked.
    Trainer passes `tokens` as the count across all accumulated batches, and then
    does not divide by the accumulation steps itself.
    """
    inputs = dict(inputs)
    labels = inputs.pop("labels")
    first = (labels != -100).int().argmax(dim=1).min().item()
    # Each logit predicts the next token, so start one before the first target.
    keep = labels.shape[1] - first + 1
    outputs = model(**inputs, logits_to_keep=keep)
    logits = outputs.logits[:, :-1]
    targets = labels[:, -(keep - 1) :].reshape(-1)
    loss = cross_entropy(
        logits.reshape(-1, logits.shape[-1]),
        targets,
        ignore_index=-100,
        reduction="sum",
    )
    return loss / (tokens or (targets != -100).sum()), outputs


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--model", required=True, type=pathlib.Path)
    parser.add_argument("--data", required=True, type=pathlib.Path)
    parser.add_argument("--tools", type=pathlib.Path, help="default: data/tools.json")
    parser.add_argument("--out", required=True, type=pathlib.Path)
    parser.add_argument("--epochs", type=float, default=2)
    parser.add_argument("--lr", type=float, default=2e-4)
    parser.add_argument("--rank", type=int, default=16)
    parser.add_argument("--batch", type=int, default=1)
    parser.add_argument("--accumulate", type=int, default=8)
    parser.add_argument("--max-length", type=int, default=16384)
    parser.add_argument("--max-examples", type=int)
    parser.add_argument("--no-merge", action="store_true")
    args = parser.parse_args()

    import datasets
    import torch
    from peft import LoraConfig, get_peft_model
    from transformers import (
        AutoModelForCausalLM,
        AutoTokenizer,
        Trainer,
        TrainingArguments,
    )

    tools = template_tools(
        json.loads((args.tools or args.data / "tools.json").read_text())
    )
    tokenizer = AutoTokenizer.from_pretrained(args.model, trust_remote_code=True)

    train, skipped = load_examples(
        args.data / "train.jsonl.zst", tools, tokenizer, args.max_length
    )
    validation, _ = load_examples(
        args.data / "validation.jsonl.zst", tools, tokenizer, args.max_length
    )
    if args.max_examples:
        train = train[: args.max_examples]
    targets = sum(sum(1 for x in e["labels"] if x != -100) for e in train)
    print(
        f"train {len(train)} turns, validation {len(validation)}, "
        f"{skipped} over --max-length, {targets} target tokens",
        flush=True,
    )

    model = AutoModelForCausalLM.from_pretrained(
        args.model,
        dtype=torch.bfloat16,
        device_map={"": 0},
        trust_remote_code=True,
    )
    model.config.use_cache = False
    model.enable_input_require_grads()
    config = LoraConfig(
        r=args.rank,
        lora_alpha=args.rank * 2,
        lora_dropout=0.05,
        target_modules=TARGET_MODULES,
        task_type="CAUSAL_LM",
    )
    model = get_peft_model(model, config)
    model.print_trainable_parameters()

    class CompletionTrainer(Trainer):
        def compute_loss(
            self, model, inputs, return_outputs=False, num_items_in_batch=None
        ):
            loss, outputs = completion_loss(
                model, inputs, torch.nn.functional.cross_entropy, num_items_in_batch
            )
            return (loss, outputs) if return_outputs else loss

    settings = TrainingArguments(
        output_dir=str(args.out / "checkpoints"),
        num_train_epochs=args.epochs,
        per_device_train_batch_size=args.batch,
        per_device_eval_batch_size=1,
        gradient_accumulation_steps=args.accumulate,
        learning_rate=args.lr,
        lr_scheduler_type="cosine",
        warmup_steps=0.05,  # a float is a fraction of the total steps
        bf16=True,
        gradient_checkpointing=True,
        gradient_checkpointing_kwargs={"use_reentrant": False},
        logging_steps=5,
        eval_strategy="epoch" if validation else "no",
        save_strategy="epoch",
        save_total_limit=2,
        report_to="none",
        remove_unused_columns=False,
        prediction_loss_only=True,
        label_names=["labels"],
    )
    trainer = CompletionTrainer(
        model=model,
        args=settings,
        train_dataset=datasets.Dataset.from_list(train),
        eval_dataset=datasets.Dataset.from_list(validation) if validation else None,
        data_collator=Collator(tokenizer.pad_token_id, torch),
    )
    trainer.train()
    print(f"peak VRAM {torch.cuda.max_memory_allocated() / 1e9:.1f} GB", flush=True)

    model.save_pretrained(args.out / "adapter")
    if args.no_merge:
        return

    merged = args.out / "merged"
    model.merge_and_unload().save_pretrained(merged)
    # LoRA changes only the weights, so everything else stays as the base model has
    # it. Saving would leave training settings behind, such as use_cache: false.
    keep = (
        "*.py",
        "config.json",
        "generation_config.json",
        "chat_template.jinja",
        "LICENSE",
        "special_tokens_map.json",
        "tokenizer*.json",
    )
    for pattern in keep:
        for path in args.model.glob(pattern):
            shutil.copy(path, merged / path.name)
    print(f"merged model in {merged}", flush=True)


if __name__ == "__main__":
    main()
