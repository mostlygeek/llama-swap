#!/usr/bin/env python3
"""OpenAI-compatible server for safetensors checkpoints and LoRA adapters.

The eval talks to /v1/chat/completions, so measuring a checkpoint needs a server
that parses tool calls. This one uses the same modeling code as training and can
load an adapter without merging it first.

It handles one request at a time, and "streams" by sending the finished response
as SSE chunks. That is enough for the eval, not for interactive use.

Run it from llama-swap so the eval can reach it:

    models:
      lfm3-stock-bf16:
        env:
          - "CUDA_DEVICE_ORDER=PCI_BUS_ID"
          - "CUDA_VISIBLE_DEVICES=2"
        cmd: |
          ~/lfm3/train/.venv/bin/python evals/docs-agent/training/serve.py
          --model ~/lfm3/base-hf --port ${PORT}
"""

import argparse
import json
import re
import sys
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# Sampling defaults from the LFM3 model card. REPEAT_WINDOW is llama.cpp's
# --repeat-last-n default. See WindowedRepetitionPenalty.
TEMPERATURE = 0.1
TOP_K = 50
REPEAT_PENALTY = 1.1
REPEAT_WINDOW = 64

TOOL_CALL = re.compile(
    r"<tool_call>\s*<function=([^>\n]+)>\s*(.*?)</function>\s*</tool_call>", re.S
)
PARAMETER = re.compile(r"<parameter=([^>\n]+)>\n?(.*?)\n?</parameter>", re.S)


def coerce(value, schema):
    """Converts a parameter's text to the type its tool schema declares."""
    if (schema or {}).get("type", "string") == "string":
        return value
    try:
        return json.loads(value)
    except json.JSONDecodeError:
        return value


def parse_output(text, tools=None, thinking=True):
    """Splits generated text into (reasoning, content, tool_calls)."""
    reasoning = ""
    if thinking:
        # The generation prompt already opened <think>, so only the close is
        # in the text. No close means it never left the reasoning.
        if "</think>" not in text:
            return text.strip(), "", []
        reasoning, text = text.split("</think>", 1)

    schemas = {}
    for tool in tools or []:
        fn = tool.get("function", tool)
        schemas[fn.get("name")] = fn.get("parameters", {}).get("properties", {})

    calls = []
    for index, match in enumerate(TOOL_CALL.finditer(text)):
        name = match.group(1).strip()
        properties = schemas.get(name, {})
        arguments = {
            key.strip(): coerce(value, properties.get(key.strip()))
            for key, value in PARAMETER.findall(match.group(2))
        }
        calls.append(
            {
                "index": index,
                "id": f"call_{uuid.uuid4().hex[:24]}",
                "type": "function",
                "function": {"name": name, "arguments": json.dumps(arguments)},
            }
        )

    return reasoning.strip(), TOOL_CALL.sub("", text).strip(), calls


def normalize_messages(messages):
    """Converts OpenAI messages to what the chat template accepts.

    The template rejects JSON-encoded tool arguments, which is how OpenAI
    clients send them.
    """
    out = []
    for message in messages:
        message = dict(message)
        if isinstance(message.get("content"), list):
            message["content"] = "".join(
                part.get("text", "") for part in message["content"]
            )
        if message.get("tool_calls"):
            calls = []
            for call in message["tool_calls"]:
                fn = dict(call["function"])
                if isinstance(fn.get("arguments"), str):
                    fn["arguments"] = json.loads(fn["arguments"] or "{}")
                calls.append({**call, "function": fn})
            message["tool_calls"] = calls
        out.append(message)
    return out


class Engine:
    def __init__(self, args):
        import torch
        from transformers import (
            AutoModelForCausalLM,
            AutoTokenizer,
            LogitsProcessor,
            LogitsProcessorList,
            StoppingCriteria,
            StoppingCriteriaList,
        )

        class WindowedRepetitionPenalty(LogitsProcessor):
            """Repetition penalty over the last `window` tokens only.

            Transformers penalizes every token in the prompt, which punishes
            copying a key out of a tool result. llama.cpp looks at a short
            window, so without this a checkpoint scores differently here than
            it does as a GGUF.
            """

            def __init__(self, penalty, window):
                self.penalty = penalty
                self.window = window

            def __call__(self, input_ids, scores):
                recent = input_ids[:, -self.window :]
                picked = torch.gather(scores, 1, recent)
                picked = torch.where(
                    picked < 0, picked * self.penalty, picked / self.penalty
                )
                return scores.scatter(1, recent, picked)

        class FirstToken(StoppingCriteria):
            """Records when the first token finished, to time the prompt."""

            def __init__(self):
                self.at = None

            def __call__(self, input_ids, scores, **kwargs):
                if self.at is None:
                    self.at = time.monotonic()
                return torch.zeros(input_ids.shape[0], dtype=torch.bool).to(
                    input_ids.device
                )

        self.torch = torch
        self.penalty_class = WindowedRepetitionPenalty
        self.processors = LogitsProcessorList
        self.first_token_class = FirstToken
        self.criteria = StoppingCriteriaList
        self.args = args
        self.lock = threading.Lock()

        self.tokenizer = AutoTokenizer.from_pretrained(
            args.model, trust_remote_code=True
        )
        self.model = AutoModelForCausalLM.from_pretrained(
            args.model,
            dtype=getattr(torch, args.dtype),
            device_map={"": 0},
            trust_remote_code=True,
        )
        if args.adapter:
            from peft import PeftModel

            self.model = PeftModel.from_pretrained(self.model, args.adapter)
        self.model.eval()

    def complete(self, body):
        tools = body.get("tools") or None
        kwargs = dict(body.get("chat_template_kwargs") or {})
        if self.args.no_thinking:
            kwargs["enable_thinking"] = False
        thinking = kwargs.get("enable_thinking", True)

        prompt = self.tokenizer.apply_chat_template(
            normalize_messages(body["messages"]),
            tools=tools,
            add_generation_prompt=True,
            tokenize=False,
            **kwargs,
        )
        inputs = self.tokenizer(
            prompt, return_tensors="pt", add_special_tokens=False
        ).to(self.model.device)
        prompt_tokens = inputs["input_ids"].shape[1]

        temperature = body.get("temperature", TEMPERATURE)
        sampling = {"do_sample": False}
        if temperature > 0:
            sampling = {
                "do_sample": True,
                "temperature": temperature,
                "top_k": body.get("top_k", TOP_K),
            }

        first = self.first_token_class()
        with self.lock:
            start = time.monotonic()
            with self.torch.no_grad():
                output = self.model.generate(
                    **inputs,
                    **sampling,
                    max_new_tokens=body.get("max_tokens") or self.args.max_new_tokens,
                    repetition_penalty=None,
                    logits_processor=self.processors(
                        [
                            self.penalty_class(
                                body.get("repeat_penalty", REPEAT_PENALTY),
                                REPEAT_WINDOW,
                            )
                        ]
                    ),
                    stopping_criteria=self.criteria([first]),
                    pad_token_id=self.tokenizer.pad_token_id,
                )
            end = time.monotonic()

        generated = output[0, prompt_tokens:]
        completion_tokens = int(generated.shape[0])
        text = self.tokenizer.decode(generated, skip_special_tokens=True)
        reasoning, content, calls = parse_output(text, tools, thinking)

        prompt_s = (first.at or end) - start
        predicted_s = end - (first.at or end)
        print(
            json.dumps(
                {
                    "prompt_tokens": prompt_tokens,
                    "completion_tokens": completion_tokens,
                    "prompt_s": round(prompt_s, 3),
                    "predicted_s": round(predicted_s, 3),
                }
            ),
            file=sys.stderr,
            flush=True,
        )

        return {
            "reasoning": reasoning,
            "content": content,
            "calls": calls,
            "usage": {
                "prompt_tokens": prompt_tokens,
                "completion_tokens": completion_tokens,
                "total_tokens": prompt_tokens + completion_tokens,
            },
        }


def build_message(result):
    message = {"role": "assistant", "content": result["content"]}
    if result["reasoning"]:
        message["reasoning_content"] = result["reasoning"]
    if result["calls"]:
        message["tool_calls"] = [
            {k: v for k, v in call.items() if k != "index"}
            for call in result["calls"]
        ]
    return message


def stream_events(result, model):
    """The SSE data payloads for one finished response."""
    base = {
        "id": f"chatcmpl-{uuid.uuid4().hex}",
        "object": "chat.completion.chunk",
        "created": int(time.time()),
        "model": model,
    }

    def chunk(delta, finish=None, **extra):
        choices = [{"index": 0, "delta": delta, "finish_reason": finish}]
        return {**base, "choices": choices, **extra}

    yield chunk({"role": "assistant", "content": ""})
    if result["reasoning"]:
        yield chunk({"reasoning_content": result["reasoning"]})
    if result["content"]:
        yield chunk({"content": result["content"]})
    if result["calls"]:
        yield chunk({"tool_calls": result["calls"]})
    yield chunk({}, "tool_calls" if result["calls"] else "stop")
    yield {**base, "choices": [], "usage": result["usage"]}


class Handler(BaseHTTPRequestHandler):
    engine = None

    def log_message(self, format, *args):
        pass

    def send_json(self, status, payload):
        data = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == "/health":
            self.send_json(200, {"status": "ok"})
        elif self.path == "/v1/models":
            model = {"id": self.engine.args.model, "object": "model"}
            self.send_json(200, {"object": "list", "data": [model]})
        else:
            self.send_json(404, {"error": {"message": "not found"}})

    def do_POST(self):
        if self.path != "/v1/chat/completions":
            self.send_json(404, {"error": {"message": "not found"}})
            return

        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        model = body.get("model", "")
        try:
            result = self.engine.complete(body)
        except Exception as error:
            self.send_json(500, {"error": {"message": str(error)}})
            return

        if not body.get("stream"):
            finish = "tool_calls" if result["calls"] else "stop"
            choice = {
                "index": 0,
                "message": build_message(result),
                "finish_reason": finish,
            }
            self.send_json(
                200,
                {
                    "id": f"chatcmpl-{uuid.uuid4().hex}",
                    "object": "chat.completion",
                    "created": int(time.time()),
                    "model": model,
                    "choices": [choice],
                    "usage": result["usage"],
                },
            )
            return

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        for event in stream_events(result, model):
            self.wfile.write(f"data: {json.dumps(event)}\n\n".encode())
        self.wfile.write(b"data: [DONE]\n\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--model", required=True, help="safetensors directory")
    parser.add_argument("--adapter", help="LoRA adapter directory")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--dtype", default="bfloat16")
    parser.add_argument("--max-new-tokens", type=int, default=4096)
    parser.add_argument(
        "--no-thinking", action="store_true", help="render with enable_thinking=False"
    )
    args = parser.parse_args()

    Handler.engine = Engine(args)
    server = ThreadingHTTPServer((args.host, args.port), Handler)
    print(f"listening on {args.host}:{args.port}", file=sys.stderr, flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
