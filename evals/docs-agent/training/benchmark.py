#!/usr/bin/env python3
"""Measures an OpenAI-compatible server directly, at increasing concurrency.

Prompts are built from docs/kb with a unique opening, so a prefix cache cannot
flatter the result. The defaults match the average Docs Agent request. Needs
only the standard library.

    python3 evals/docs-agent/training/benchmark.py \
        --base-url http://127.0.0.1:18200 --model lfm3-stock-bf16
"""

import argparse
import json
import pathlib
import statistics
import time
import urllib.request
import uuid
from concurrent.futures import ThreadPoolExecutor

KB = pathlib.Path(__file__).resolve().parents[3] / "docs" / "kb"


def load_text():
    return "\n".join(p.read_text() for p in sorted(KB.rglob("*.md")))


def request(args, text, offset):
    # About 4 characters per token. usage reports the real count.
    chars = args.prompt_tokens * 4
    body = text[offset % max(1, len(text) - chars) :][:chars]
    payload = {
        "model": args.model,
        "messages": [
            {"role": "user", "content": f"{uuid.uuid4()}\n{body}\n\nSummarize this."}
        ],
        "max_tokens": args.max_tokens,
        "ignore_eos": True,
        "temperature": 0.1,
    }
    req = urllib.request.Request(
        f"{args.base_url}/v1/chat/completions",
        json.dumps(payload).encode(),
        {"Content-Type": "application/json"},
    )
    start = time.monotonic()
    with urllib.request.urlopen(req, timeout=args.timeout) as response:
        usage = json.load(response)["usage"]
    return time.monotonic() - start, usage


def run(args, text, concurrency):
    count = max(concurrency * args.rounds, args.min_requests)
    offsets = [i * 7919 for i in range(count)]
    start = time.monotonic()
    with ThreadPoolExecutor(concurrency) as pool:
        results = list(pool.map(lambda o: request(args, text, o), offsets))
    elapsed = time.monotonic() - start

    latencies = sorted(r[0] for r in results)
    return {
        "concurrency": concurrency,
        "requests": count,
        "prompt_tokens": round(
            statistics.mean(r[1]["prompt_tokens"] for r in results)
        ),
        "generated_tok_s": round(
            sum(r[1]["completion_tokens"] for r in results) / elapsed, 1
        ),
        "requests_s": round(count / elapsed, 2),
        "latency_p50_s": round(latencies[len(latencies) // 2], 1),
        "latency_max_s": round(latencies[-1], 1),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--concurrency", type=int, nargs="+", default=[1, 4, 8, 16, 32])
    parser.add_argument("--prompt-tokens", type=int, default=6800)
    parser.add_argument("--max-tokens", type=int, default=460)
    parser.add_argument("--rounds", type=int, default=3)
    parser.add_argument("--min-requests", type=int, default=8)
    parser.add_argument("--timeout", type=int, default=600)
    args = parser.parse_args()

    text = load_text()
    for concurrency in args.concurrency:
        print(json.dumps(run(args, text, concurrency)), flush=True)


if __name__ == "__main__":
    main()
