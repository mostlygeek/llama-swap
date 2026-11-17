#!/usr/bin/env python3
"""Asks the teacher to write user questions, and drops any near an eval case.

Questions come from docs/kb and the training fixture, never from the eval cases.
The eval questions are read only to reject lookalikes, are never sent to the
teacher, and are never printed. Finished sources are skipped on a re-run.

Run with a Python that has pyyaml:

    ~/lfm3/train/.venv/bin/python evals/docs-agent/training/questions.py \
        --base-url http://127.0.0.1:18081 --model sippy/Q3.8-27B-rad \
        --out ~/lfm3/data/questions.jsonl
"""

import argparse
import json
import pathlib
import re
import threading
import urllib.request
from concurrent.futures import ThreadPoolExecutor

import yaml

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parents[2]
KB = ROOT / "docs" / "kb"
EVAL_DIRS = [HERE.parent / "cases", HERE.parent / "cases-holdout"]
FIXTURE = HERE / "fixture" / "models.yaml"

STOPWORDS = set(
    """a an and are as at be but by can do does for from how i if in is it its my
    of on or so that the their this to use using what when where which why will
    with would you your llama swap""".split()
)

STYLE = """Write them the way real users write: some terse, some with a little
context about their setup, some describing a symptom rather than naming a setting.
Vary the openings. Do not mention "the docs" or "the guide". Each must stand alone.

Reply with only a JSON array of strings."""


def tokens(text):
    words = re.findall(r"[a-z0-9_]+", text.lower())
    return {w.rstrip("s") for w in words if w not in STOPWORDS}


def eval_questions():
    found = []
    for directory in EVAL_DIRS:
        for path in directory.rglob("*.yaml"):
            for case in yaml.safe_load(path.read_text()) or []:
                found.append(tokens(case["question"]))
    return found


def too_close(question, seen, threshold):
    words = tokens(question)
    for other in seen:
        union = words | other
        if union and len(words & other) / len(union) >= threshold:
            return True
    return False


def ask(args, prompt):
    payload = {
        "model": args.model,
        "messages": [{"role": "user", "content": prompt}],
        "max_tokens": args.max_tokens,
    }
    request = urllib.request.Request(
        f"{args.base_url}/v1/chat/completions",
        json.dumps(payload).encode(),
        {"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(request, timeout=args.timeout) as response:
        message = json.load(response)["choices"][0]["message"]
    content = message.get("content") or ""
    start, end = content.find("["), content.rfind("]")
    if start < 0 or end < 0:
        raise ValueError(f"no JSON array in: {content[:200]}")
    return [q.strip() for q in json.loads(content[start : end + 1]) if isinstance(q, str)]


def doc_id(path):
    return str(path.relative_to(KB).with_suffix(""))


def documents():
    files = [p for p in sorted(KB.rglob("*.md")) if p.name != "README.md"]
    return {doc_id(p): p.read_text() for p in files}


def answerable_jobs(docs, count):
    for source, text in docs.items():
        prompt = f"""Below is a page from the llama-swap documentation. Write {count} different questions a llama-swap user might ask that this page answers. Cover different parts of the page, not only the first section.

{STYLE}

<page>
{text}
</page>"""
        yield source, "answerable", prompt


def config_jobs(chunk, count):
    text = FIXTURE.read_text()
    blocks = re.split(r"(?m)^(?=  train-[^\n]*:\s*$)", text[text.index("models:") :])
    blocks = [b for b in blocks if b.strip() and not b.startswith("models:")]
    for index in range(0, len(blocks), chunk):
        part = "".join(blocks[index : index + chunk])
        prompt = f"""Below is part of the configuration running on a llama-swap server. Write {count} different questions asking about specific values in it, such as one model's ttl, aliases, filters, metadata or capabilities, or which models share some property. Refer to models by their exact ids and say "on this server", "here" or similar, so it is clear the question is about the running setup.

{STYLE}

<config>
{part}
</config>"""
        yield f"fixture/{index // chunk}", "running-config", prompt


def nonexistent_jobs(docs, batches, count):
    titles = []
    for source, text in docs.items():
        match = re.search(r"(?m)^title:\s*(.+)$", text)
        titles.append(f"- {match.group(1) if match else source}")
    topics = "\n".join(titles)
    areas = [
        "authentication and users",
        "billing, quotas and scheduling",
        "storage and backups",
        "model management and downloads",
        "networking and protocols",
        "the web UI and notifications",
        "GPU and hardware control",
        "routing and load balancing",
    ]
    for area in areas[:batches]:
        prompt = f"""llama-swap is a proxy that swaps local AI model servers on demand. These are the topics its documentation covers:

{topics}

Write {count} different questions from users asking how to configure or use a feature in the area of {area} that llama-swap most likely does NOT have. Make them plausible, the kind of thing a user would assume exists.

{STYLE}"""
        yield f"nonexistent/{area}", "nonexistent", prompt


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--out", required=True, type=pathlib.Path)
    parser.add_argument("--concurrency", type=int, default=4)
    parser.add_argument("--per-doc", type=int, default=8)
    parser.add_argument("--per-config", type=int, default=8)
    parser.add_argument("--per-area", type=int, default=10)
    parser.add_argument("--threshold", type=float, default=0.5)
    parser.add_argument("--max-tokens", type=int, default=8000)
    parser.add_argument("--timeout", type=int, default=900)
    parser.add_argument("--only", help="only sources containing this text")
    parser.add_argument("--tag", help="prefix for source ids, to run a source again")
    parser.add_argument(
        "--avoid",
        action="append",
        default=[],
        type=pathlib.Path,
        help="questions file from an earlier batch not to repeat",
    )
    args = parser.parse_args()

    docs = documents()
    jobs = [
        *answerable_jobs(docs, args.per_doc),
        *config_jobs(4, args.per_config),
        *nonexistent_jobs(docs, 8, args.per_area),
    ]
    if args.only:
        jobs = [job for job in jobs if args.only in job[0]]
    if args.tag:
        jobs = [(f"{args.tag}/{source}", *rest) for source, *rest in jobs]

    args.out.parent.mkdir(parents=True, exist_ok=True)
    done = set()
    if args.out.exists():
        done = {json.loads(line)["source"] for line in args.out.open()}
    jobs = [job for job in jobs if job[0] not in done]
    print(f"{len(jobs)} sources to do, {len(done)} already done", flush=True)

    blocked = eval_questions()
    lock = threading.Lock()
    totals = {"kept": 0, "near_eval": 0, "duplicate": 0, "failed": 0}
    seen = [
        tokens(json.loads(line)["question"])
        for path in args.avoid
        for line in path.open()
    ]

    def work(job):
        source, kind, prompt = job
        try:
            questions = ask(args, prompt)
        except Exception as error:
            with lock:
                totals["failed"] += 1
            print(f"FAILED {source}: {error}", flush=True)
            return
        with lock:
            kept = 0
            with args.out.open("a") as out:
                for index, question in enumerate(questions):
                    if too_close(question, blocked, args.threshold):
                        totals["near_eval"] += 1
                    elif too_close(question, seen, 0.8):
                        totals["duplicate"] += 1
                    else:
                        seen.append(tokens(question))
                        record = {
                            "id": f"{source}#{index}",
                            "source": source,
                            "kind": kind,
                            "question": question,
                        }
                        out.write(json.dumps(record) + "\n")
                        totals["kept"] += 1
                        kept += 1
            print(f"{source}: kept {kept} of {len(questions)}", flush=True)

    with ThreadPoolExecutor(args.concurrency) as pool:
        list(pool.map(work, jobs))
    print(json.dumps(totals), flush=True)


if __name__ == "__main__":
    main()
