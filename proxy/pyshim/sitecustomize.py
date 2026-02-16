"""
Compatibility shim for swebench inference used by llama-swap benchy jobs.

Enabled only when LLAMA_SWAP_SWEBENCH_TEXT_COMPAT=1.
"""

import os


def _pick_text_column(dataset):
    columns = set(getattr(dataset, "column_names", []) or [])
    for candidate in ("text", "problem_statement", "prompt", "question"):
        if candidate in columns:
            return candidate
    return "text"


def _patch_swebench_main() -> None:
    if os.environ.get("LLAMA_SWAP_SWEBENCH_TEXT_COMPAT") != "1":
        return

    try:
        import tiktoken
    except Exception:
        tiktoken = None

    if tiktoken is not None:
        original_encoding_for_model = tiktoken.encoding_for_model

        def compat_encoding_for_model(model_name):  # type: ignore[no-untyped-def]
            try:
                return original_encoding_for_model(model_name)
            except Exception:
                return tiktoken.get_encoding("cl100k_base")

        tiktoken.encoding_for_model = compat_encoding_for_model  # type: ignore[assignment]

    try:
        import swebench.inference.run_api as api
    except Exception:
        return

    original_openai_inference = api.openai_inference

    def compat_openai_inference(*args, **kwargs):  # type: ignore[no-untyped-def]
        dataset = kwargs.get("test_dataset")
        if dataset is None and len(args) > 0:
            dataset = args[0]
        if dataset is not None:
            columns = set(getattr(dataset, "column_names", []) or [])
            if "text" not in columns:
                fallback = _pick_text_column(dataset)
                if fallback != "text":
                    dataset = dataset.add_column("text", dataset[fallback])
                    if len(args) > 0:
                        args = (dataset, *args[1:])
                    else:
                        kwargs["test_dataset"] = dataset
        return original_openai_inference(*args, **kwargs)

    api.openai_inference = compat_openai_inference

    def compat_main(
        dataset_name_or_path,
        split,
        model_name_or_path,
        shard_id,
        num_shards,
        output_dir,
        model_args,
        max_cost,
    ):
        if shard_id is None and num_shards is not None:
            api.logger.warning(
                f"Received num_shards={num_shards} but shard_id is None, ignoring"
            )
        if shard_id is not None and num_shards is None:
            api.logger.warning(
                f"Received shard_id={shard_id} but num_shards is None, ignoring"
            )

        model_args = api.parse_model_args(model_args)
        model_nickname = model_name_or_path
        if "checkpoint" in api.Path(model_name_or_path).name:
            model_nickname = api.Path(model_name_or_path).parent.name
        else:
            model_nickname = api.Path(model_name_or_path).name

        output_file = f"{model_nickname}__{dataset_name_or_path.split('/')[-1]}__{split}"
        if shard_id is not None and num_shards is not None:
            output_file += f"__shard-{shard_id}__num_shards-{num_shards}"
        output_file = api.Path(output_dir, output_file + ".jsonl")
        api.logger.info(f"Will write to {output_file}")

        existing_ids = set()
        if api.os.path.exists(output_file):
            with open(output_file) as f:
                for line in f:
                    data = api.json.loads(line)
                    existing_ids.add(data["instance_id"])
        api.logger.info(f"Read {len(existing_ids)} already completed ids from {output_file}")

        if api.Path(dataset_name_or_path).exists():
            dataset = api.load_from_disk(dataset_name_or_path)
        else:
            dataset = api.load_dataset(dataset_name_or_path)
        if split not in dataset:
            raise ValueError(f"Invalid split {split} for dataset {dataset_name_or_path}")

        dataset = dataset[split]
        text_col = _pick_text_column(dataset)
        lens = api.np.array(list(map(len, dataset[text_col])))
        dataset = dataset.select(api.np.argsort(lens))

        if len(existing_ids) > 0:
            dataset = dataset.filter(
                lambda x: x["instance_id"] not in existing_ids,
                desc="Filtering out existing ids",
                load_from_cache_file=False,
            )
        if shard_id is not None and num_shards is not None:
            dataset = dataset.shard(num_shards, shard_id, contiguous=True)

        inference_args = {
            "test_dataset": dataset,
            "model_name_or_path": model_name_or_path,
            "output_file": output_file,
            "model_args": model_args,
            "existing_ids": existing_ids,
            "max_cost": max_cost,
        }

        if str(model_name_or_path).startswith("claude"):
            api.anthropic_inference(**inference_args)
        else:
            api.openai_inference(**inference_args)
        api.logger.info("Done!")

    api.main = compat_main


_patch_swebench_main()
