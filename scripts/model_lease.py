#!/usr/bin/env python3
"""Client for the llama-swap model-lease API.

A lease protects a model from being evicted while another model loads. It is the
refuse-don't-break contract: with a live lease, a competing load is refused
(503 + blocked_by) instead of silently evicting your model. There is NO
heartbeat; the server expires the lease after its TTL, so a clean exit should
release and a crash is caught by the TTL.

Use as a context manager around a work session:

    from model_lease import model_lease

    with model_lease("qwen3.5-9b-vision", holder="run_tagging.py",
                     reason="tag project-39", ttl="5h") as lease:
        # lease.header() -> {"X-Llama-Swap-Lease": "<id>"} for your requests
        run_batch(extra_headers=lease.header())

Or as a CLI:

    llama-swap-lease acquire qwen3.5-9b-vision --holder run.py --reason batch --ttl 4h
    llama-swap-lease ls
    llama-swap-lease can-load qwen3.5-9b-vision
    llama-swap-lease extend <id> --ttl 2h
    llama-swap-lease release <id>
    llama-swap-lease kill --model qwen3.5-9b-vision
"""

from __future__ import annotations

import argparse
import contextlib
import json
import os
import signal
import sys
import urllib.error
import urllib.request
from typing import Any, Optional

DEFAULT_BASE_URL = os.environ.get("LLAMA_SWAP_URL", "http://localhost:8080")
LEASE_HEADER = "X-Llama-Swap-Lease"


class LeaseError(RuntimeError):
    """A lease API call failed. `status` and `body` carry the HTTP detail."""

    def __init__(self, message: str, status: int = 0, body: Any = None):
        super().__init__(message)
        self.status = status
        self.body = body


def _request(method: str, url: str, api_key: Optional[str], payload: Optional[dict] = None) -> tuple[int, Any]:
    data = None
    headers = {"Accept": "application/json"}
    if payload is not None:
        data = json.dumps(payload).encode()
        headers["Content-Type"] = "application/json"
    if api_key:
        headers["Authorization"] = f"Bearer {api_key}"
    req = urllib.request.Request(url, data=data, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req) as resp:
            raw = resp.read()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as e:
        raw = e.read()
        body = None
        with contextlib.suppress(Exception):
            body = json.loads(raw) if raw else None
        return e.code, body
    except urllib.error.URLError as e:
        raise LeaseError(f"cannot reach llama-swap at {url}: {e.reason}") from e


class Lease:
    """A live lease handle. `header()` gives the request header to tag work with."""

    def __init__(self, base_url: str, api_key: Optional[str], data: dict):
        self._base_url = base_url.rstrip("/")
        self._api_key = api_key
        self.id = data["id"]
        self.model = data["model"]
        self.data = data

    def header(self) -> dict[str, str]:
        return {LEASE_HEADER: self.id}

    def extend(self, ttl: str) -> "Lease":
        status, body = _request(
            "POST", f"{self._base_url}/leases/{self.id}/extend", self._api_key, {"ttl": ttl}
        )
        if status != 200:
            raise LeaseError(f"extend failed: {body}", status, body)
        self.data = body
        return self

    def release(self) -> bool:
        status, _ = _request("DELETE", f"{self._base_url}/leases/{self.id}", self._api_key)
        # 204 released; 404 already gone (expired/killed) — both mean "not held".
        return status in (204, 404)


def acquire(model: str, holder: str = "", reason: str = "", ttl: str = "",
            base_url: str = DEFAULT_BASE_URL, api_key: Optional[str] = None) -> Lease:
    payload = {"model": model, "holder": holder, "reason": reason, "ttl": ttl}
    status, body = _request("POST", f"{base_url.rstrip('/')}/leases", api_key, payload)
    if status != 201:
        raise LeaseError(f"acquire failed ({status}): {body}", status, body)
    return Lease(base_url, api_key, body)


@contextlib.contextmanager
def model_lease(model: str, holder: str = "", reason: str = "", ttl: str = "",
                base_url: str = DEFAULT_BASE_URL, api_key: Optional[str] = None):
    """Acquire on enter, release on exit (also on SIGTERM). TTL is the crash backstop."""
    lease = acquire(model, holder=holder, reason=reason, ttl=ttl, base_url=base_url, api_key=api_key)

    released = {"done": False}

    def _release_once(*_):
        if not released["done"]:
            released["done"] = True
            with contextlib.suppress(Exception):
                lease.release()

    prev = signal.getsignal(signal.SIGTERM)

    def _on_sigterm(signum, frame):
        _release_once()
        if callable(prev):
            prev(signum, frame)
        else:
            raise SystemExit(143)

    with contextlib.suppress(ValueError):  # not in main thread => cannot set signal
        signal.signal(signal.SIGTERM, _on_sigterm)
    try:
        yield lease
    finally:
        _release_once()
        with contextlib.suppress(ValueError):
            signal.signal(signal.SIGTERM, prev)


# --------------------------------------------------------------------------- CLI


def _print(obj: Any) -> None:
    print(json.dumps(obj, indent=2, sort_keys=True))


def _cli(argv: list[str]) -> int:
    p = argparse.ArgumentParser(prog="llama-swap-lease", description=__doc__,
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--url", default=DEFAULT_BASE_URL, help="llama-swap base URL")
    p.add_argument("--api-key", default=os.environ.get("LLAMA_SWAP_API_KEY"), help="API key (Bearer)")
    sub = p.add_subparsers(dest="cmd", required=True)

    a = sub.add_parser("acquire", help="acquire a lease")
    a.add_argument("model")
    a.add_argument("--holder", default="")
    a.add_argument("--reason", default="")
    a.add_argument("--ttl", default="", help="Go duration, e.g. 4h or 30m (default: server max)")

    r = sub.add_parser("release", help="release a lease by id")
    r.add_argument("id")

    e = sub.add_parser("extend", help="extend a lease by id")
    e.add_argument("id")
    e.add_argument("--ttl", required=True)

    k = sub.add_parser("kill", help="force-remove leases (exactly one selector)")
    g = k.add_mutually_exclusive_group(required=True)
    g.add_argument("--id")
    g.add_argument("--model")
    g.add_argument("--holder")

    sub.add_parser("ls", help="list live leases")

    c = sub.add_parser("can-load", help="preflight: what happens if I load this model")
    c.add_argument("model")

    args = p.parse_args(argv)
    base = args.url.rstrip("/")

    try:
        if args.cmd == "acquire":
            lease = acquire(args.model, args.holder, args.reason, args.ttl, base, args.api_key)
            _print(lease.data)
        elif args.cmd == "release":
            status, _ = _request("DELETE", f"{base}/leases/{args.id}", args.api_key)
            if status not in (204, 404):
                print(f"release failed: {status}", file=sys.stderr)
                return 1
            _print({"released": status == 204, "id": args.id})
        elif args.cmd == "extend":
            status, body = _request("POST", f"{base}/leases/{args.id}/extend", args.api_key, {"ttl": args.ttl})
            if status != 200:
                print(f"extend failed ({status}): {body}", file=sys.stderr)
                return 1
            _print(body)
        elif args.cmd == "kill":
            sel = {"id": args.id or "", "model": args.model or "", "holder": args.holder or ""}
            status, body = _request("POST", f"{base}/leases/kill", args.api_key, sel)
            if status != 200:
                print(f"kill failed ({status}): {body}", file=sys.stderr)
                return 1
            _print(body)
        elif args.cmd == "ls":
            status, body = _request("GET", f"{base}/leases", args.api_key)
            if status != 200:
                print(f"list failed ({status}): {body}", file=sys.stderr)
                return 1
            _print(body)
        elif args.cmd == "can-load":
            status, body = _request("GET", f"{base}/leases/can-load/{args.model}", args.api_key)
            if status != 200:
                print(f"can-load failed ({status}): {body}", file=sys.stderr)
                return 1
            _print(body)
    except LeaseError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(_cli(sys.argv[1:]))
