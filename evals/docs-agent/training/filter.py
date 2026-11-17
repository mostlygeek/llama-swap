#!/usr/bin/env python3
"""Keeps the recorded conversations worth training on, and splits them.

A conversation is kept only if it shows the behavior the small model should copy:
it finished, took few steps, repeated no tool calls, recovered from any failed one,
every config key in its answer came from a tool result, every outside link is real,
and its YAML fits the config schema. Run with a Python that has pyyaml and
zstandard:

    ~/lfm3/train/.venv/bin/python evals/docs-agent/training/filter.py \
        ~/lfm3/data/conversations.jsonl --report   # counts only, writes nothing
    ~/lfm3/train/.venv/bin/python evals/docs-agent/training/filter.py \
        ~/lfm3/data/conversations.jsonl --out evals/docs-agent/training/data-next \
        --forbid ~/lfm3/forbid.txt
"""

import argparse
import collections
import datetime
import hashlib
import json
import pathlib
import re
import shutil
import statistics
import subprocess

import yaml
import zstandard

BACKTICK = re.compile(r"`([^`\n]+)`")
# A single token that looks like a key, flag or endpoint, not prose or code.
KEY = re.compile(r"^[-/.]?[A-Za-z][\w.-]*(/[\w.-]+)*:?$")
KEY_SHAPED = re.compile(r"[a-z][A-Z]|[._/]|^-")
NEGATIVE = re.compile(
    r"\b(does not|doesn't|do not|don't|no built-in|no such|not support|"
    r"isn't|is not|not available|no native|cannot|can't|has no|have no)\b",
    re.I,
)
# llama-swap's sendLoadingState text, which a peer streams as reasoning.
LOADING = "llama-swap loading model"
SCHEMA = json.loads(
    (pathlib.Path(__file__).resolve().parents[3] / "config-schema.json").read_text()
)
FENCE = re.compile(r"```\w*\n(.*?)```", re.S)
YAML_FENCE = re.compile(r"```ya?ml\n(.*?)```", re.S)
URL = re.compile(r"https?://[^\s)\]>`\"']+")
# Example addresses, which no tool result needs to contain.
LOCAL_HOST = re.compile(
    r"^(localhost|[\d.]+|[\w-]+|.*\.(local|example|internal|ts\.net|svc)|"
    r"(.*\.)?example\.(com|org)|.*[<${].*)(:|$)"
)
FLAG = re.compile(r"(?<![\w-])--[a-z][a-z0-9-]+")


def assistant_messages(record):
    return [m for m in record["messages"] if m["role"] == "assistant"]


def tool_messages(record):
    return [m for m in record["messages"] if m["role"] == "tool"]


def sources(record):
    """Everything an answer may quote: tool results, and the question."""
    return "\n".join(m["content"] for m in tool_messages(record)) + record["question"]


def invented_urls(record):
    """Links to outside hosts that no tool result contains."""
    answer = assistant_messages(record)[-1].get("content") or ""
    source = sources(record)
    invented = []
    for url in URL.findall(answer):
        url = url.rstrip(".,;:").split("#")[0]
        host = url.split("/")[2]
        if not LOCAL_HOST.match(host) and url not in source:
            invented.append(url)
    return invented


def resolve(schema):
    """Follows $ref, and joins the properties of allOf, anyOf and oneOf branches."""
    while "$ref" in schema:
        schema = SCHEMA["definitions"][schema["$ref"].rsplit("/", 1)[1]]
    joined = dict(schema)
    joined["properties"] = dict(schema.get("properties", {}))
    for branch in ("allOf", "anyOf", "oneOf"):
        for option in schema.get(branch, []):
            option = resolve(option)
            joined["properties"].update(option["properties"])
            if "additionalProperties" in option:
                joined.setdefault(
                    "additionalProperties", option["additionalProperties"]
                )
    return joined


def unknown_keys(node, schema, path=""):
    """Paths in a YAML document that the schema does not define."""
    schema = resolve(schema)
    if isinstance(node, list):
        items = schema.get("items")
        if isinstance(items, dict):
            return [k for item in node for k in unknown_keys(item, items, path + "[]")]
        return []
    if not isinstance(node, dict) or not schema["properties"]:
        # A map of user-chosen names, such as model ids, has no properties.
        extra = schema.get("additionalProperties")
        if isinstance(node, dict) and isinstance(extra, dict):
            return [
                k for key, value in node.items()
                for k in unknown_keys(value, extra, f"{path}.*")
            ]
        return []
    unknown = []
    for key, value in node.items():
        if key in schema["properties"]:
            unknown += unknown_keys(value, schema["properties"][key], f"{path}.{key}")
        elif isinstance(schema.get("additionalProperties"), dict):
            unknown += unknown_keys(value, schema["additionalProperties"], f"{path}.*")
        elif schema.get("additionalProperties") is not True:
            unknown.append(f"{path}.{key}".lstrip("."))
    return unknown


def schema_errors(record):
    """YAML keys in the answer's llama-swap config that the schema rejects.

    Only whole configs and model blocks can be placed. Anything else, such as a
    Kubernetes manifest or a nested fragment, is skipped.
    """
    answer = assistant_messages(record)[-1].get("content") or ""
    model = SCHEMA["properties"]["models"]["additionalProperties"]
    errors = []
    for block in YAML_FENCE.findall(answer):
        try:
            document = yaml.safe_load(block)
        except yaml.YAMLError:
            continue
        if not isinstance(document, dict):
            continue
        if set(document) <= set(SCHEMA["properties"]):
            errors += unknown_keys(document, SCHEMA)
        elif set(document) <= set(resolve(model)["properties"]):
            errors += unknown_keys(document, model, "models.*")
    return errors


def failed_turns(record):
    """Indexes of assistant messages whose tool call failed."""
    failed = []
    assistant = None
    for index, message in enumerate(record["messages"]):
        if message["role"] == "assistant":
            assistant = index
        elif message.get("toolOk") is False and assistant not in failed:
            failed.append(assistant)
    return failed


def recovered(record, limit):
    """At most `limit` tool failures, none of them the last tool call."""
    tools = tool_messages(record)
    failures = sum(m.get("toolOk") is False for m in tools)
    return failures <= limit and (not tools or tools[-1].get("toolOk") is not False)


def invented_keys(record):
    """Key-like tokens in the answer that no tool result or question contains."""
    answer = assistant_messages(record)[-1].get("content") or ""
    source = sources(record).lower()
    if record["kind"] == "nonexistent":
        # A search term may be named to say that the search found nothing.
        calls = (
            call["function"]["arguments"]
            for message in assistant_messages(record)
            for call in message.get("tool_calls") or []
        )
        source += "\n" + "\n".join(calls).lower()
    invented = []
    for token in BACKTICK.findall(answer):
        token = token.strip().rstrip(":")
        # "..." is a placeholder, as in /api/models/..., so match what precedes it.
        token = token.split("...")[0]
        if not KEY.match(token) or token.lower() in source:
            continue
        # An answer that a feature is missing may name an outside tool, such as
        # rclone. Only a key-shaped name is a claim about llama-swap.
        if record["kind"] == "nonexistent" and not KEY_SHAPED.search(token):
            continue
        invented.append(token)
    # YAML keys are not checked here: most unknown ones are placeholder model ids.
    for block in FENCE.findall(answer):
        invented += [flag for flag in FLAG.findall(block) if flag not in source]
    return invented


def reject_reason(record, args):
    """The first check a conversation fails, or None."""
    if args.forbidden and any(text in json.dumps(record) for text in args.forbidden):
        return "local setup in conversation"
    if record.get("doneReason") != "stop" or record.get("error"):
        return "unfinished"
    assistants = assistant_messages(record)
    if not assistants or not (assistants[-1].get("content") or "").strip():
        return "no answer"
    if any(LOADING in (m.get("reasoning_content") or "") for m in assistants):
        return "loading text in reasoning"
    if any(LOADING in (m.get("content") or "") for m in assistants):
        return "loading text in content"
    if not tool_messages(record):
        return "no tools used"
    # Showing that something does not exist takes more searching.
    extra = args.nonexistent_extra if record["kind"] == "nonexistent" else 0
    if record["iterations"] > args.max_steps + extra:
        return "too many steps"
    if not recovered(record, args.max_tool_errors):
        return "tool error"
    calls = [
        (c["function"]["name"], c["function"]["arguments"])
        for m in assistants
        for c in m.get("tool_calls") or []
    ]
    if len(calls) != len(set(calls)):
        return "repeated call"
    if len(calls) > args.max_calls + extra:
        return "too many calls"
    if max(len(m.get("reasoning_content") or "") for m in assistants) > args.max_reasoning:
        return "reasoning too long"
    if invented_keys(record):
        return "key not in tool results"
    if invented_urls(record):
        return "url not in tool results"
    if schema_errors(record):
        return "yaml not in schema"
    if record["kind"] == "nonexistent":
        if not NEGATIVE.search(assistants[-1]["content"][:600]):
            return "did not say it is missing"
    return None


def clean(record):
    keep = ("id", "source", "kind", "messages")
    out = {key: record[key] for key in keep}
    out["messages"] = []
    failed = failed_turns(record)
    for index, message in enumerate(record["messages"]):
        message = {
            k: v for k, v in message.items() if k not in ("toolOk", "toolDurationMs")
        }
        if index in failed:
            # Context for the recovery after it, but not a turn to learn from.
            message["skip"] = True
        out["messages"].append(message)
    return out


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument(
        "conversations",
        nargs="+",
        type=pathlib.Path,
        help="files in order of preference: the first kept answer to a question wins",
    )
    parser.add_argument("--out", type=pathlib.Path)
    parser.add_argument("--report", action="store_true")
    parser.add_argument("--max-steps", type=int, default=6)
    parser.add_argument("--max-calls", type=int, default=8)
    parser.add_argument("--max-reasoning", type=int, default=1500)
    parser.add_argument("--max-tool-errors", type=int, default=1)
    parser.add_argument("--nonexistent-extra", type=int, default=4)
    parser.add_argument("--validation-percent", type=int, default=10)
    parser.add_argument(
        "--forbid",
        type=pathlib.Path,
        help="file of text, one per line, that must not reach the data, such as "
        "private addresses. Keep it out of the repository.",
    )
    args = parser.parse_args()

    args.forbidden = args.forbid.read_text().split() if args.forbid else []
    records = [
        json.loads(line) for path in args.conversations for line in path.open()
    ]
    reasons = collections.Counter()
    kept = {}
    for record in records:
        reason = reject_reason(record, args)
        reasons[reason or "kept"] += 1
        if reason is None:
            kept.setdefault(record["id"], record)
    kept = list(kept.values())

    steps = [r["iterations"] for r in records]
    print(f"conversations {len(records)}, kept {len(kept)}")
    print(json.dumps(reasons.most_common()))
    print(f"steps median {statistics.median(steps)}, max {max(steps)}")
    print(
        "kept by kind",
        json.dumps(collections.Counter(r["kind"] for r in kept).most_common()),
    )

    if args.report or not args.out:
        for record in records:
            invented = invented_keys(record) if assistant_messages(record) else []
            if invented:
                print(f"  invented {record['id']}: {invented[:6]}")
            if assistant_messages(record):
                urls, keys = invented_urls(record), schema_errors(record)
                if urls:
                    print(f"  url {record['id']}: {urls[:3]}")
                if keys:
                    print(f"  schema {record['id']}: {keys[:6]}")
        return

    args.out.mkdir(parents=True, exist_ok=True)
    splits = {"train": [], "validation": []}
    for record in kept:
        digest = hashlib.sha256(record["id"].encode()).digest()[0]
        held = digest * 100 // 256 < args.validation_percent
        line = json.dumps(clean(record), ensure_ascii=False) + "\n"
        splits["validation" if held else "train"].append(line)
    for name, lines in splits.items():
        data = zstandard.ZstdCompressor(level=19).compress("".join(lines).encode())
        (args.out / f"{name}.jsonl.zst").write_bytes(data)

    tools = args.out / "tools.json"
    shutil.copy(f"{args.conversations[0]}.tools.json", tools)
    prompt = next(m["content"] for m in kept[0]["messages"] if m["role"] == "system")
    commit = subprocess.run(
        ["git", "rev-parse", "HEAD"], capture_output=True, text=True, check=True
    )
    manifest = {
        "kb_commit": commit.stdout.strip(),
        "system_prompt_sha256": hashlib.sha256(prompt.encode()).hexdigest(),
        "tools_sha256": hashlib.sha256(tools.read_bytes()).hexdigest(),
        # A peer's prefix is a local host's name, not the teacher's.
        "teacher_conversations": collections.Counter(
            r["model"].split("/")[-1] for r in kept
        ),
        "generated_at": datetime.datetime.now(datetime.UTC).isoformat(timespec="seconds"),
        "kept_by_kind": collections.Counter(r["kind"] for r in kept),
        **{name: len(lines) for name, lines in splits.items()},
    }
    (args.out / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")


if __name__ == "__main__":
    main()
