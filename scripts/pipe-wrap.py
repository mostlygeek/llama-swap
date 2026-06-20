#!/usr/bin/env python3
"""
pipe-wrap.py — HTTP/OpenAI-compatible wrapper for stdin/stdout CLI inference tools.

Keeps a subprocess alive, writes prompts to stdin, and reads responses from
stdout until a configurable delimiter appears (default: \n> as printed by
llama-diffusion-cli and similar llama.cpp conversation tools).

Exposes /health and /v1/chat/completions so it works as a llama-swap backend.

Usage:
  pipe-wrap.py [options] -- <command> [command-args...]

Examples:
  # diffusion-cli as a llama-swap backend
  pipe-wrap.py --port 8080 --model-name gemma3-diffusion \\
    -- llama-diffusion-cli --model /models/gemma3-diffusion.gguf --conversation

  # any other REPL-style CLI tool
  pipe-wrap.py --port 8081 --delimiter "\\n>>> " \\
    -- some-other-tool --interactive
"""

import argparse
import json
import queue
import subprocess
import sys
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


# ---------------------------------------------------------------------------
# Prompt formatting
# ---------------------------------------------------------------------------

def format_messages(messages, template):
    """Convert an OpenAI messages list into a single prompt string."""
    if template == "last":
        # System message prepended, then the most recent user turn only.
        # Best for stateful conversation-mode CLIs that keep their own history.
        system = next((m["content"] for m in messages if m.get("role") == "system"), "")
        user = next((m["content"] for m in reversed(messages) if m.get("role") == "user"), "")
        return f"{system}\n\n{user}".strip() if system else user

    if template == "simple":
        # Human-readable role: content lines, good for generic chat CLIs.
        role_map = {"system": "System", "user": "User", "assistant": "Assistant"}
        lines = [f"{role_map.get(m['role'], m['role'])}: {m['content']}" for m in messages]
        return "\n".join(lines) + "\nAssistant:"

    if template == "chatml":
        # <|im_start|> tokens used by many instruction-tuned models.
        parts = [f"<|im_start|>{m['role']}\n{m['content']}<|im_end|>" for m in messages]
        return "\n".join(parts) + "\n<|im_start|>assistant\n"

    raise ValueError(f"unknown template: {template!r}")


# ---------------------------------------------------------------------------
# Subprocess manager
# ---------------------------------------------------------------------------

class CLIProcess:
    """Wraps a persistent subprocess and serialises prompt/response I/O."""

    def __init__(self, cmd, delimiter, startup_timeout):
        self._delim = delimiter.encode()
        self._lock = threading.Lock()
        self._proc = subprocess.Popen(
            cmd,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=sys.stderr,  # let model log output flow through
        )
        _log(f"started pid={self._proc.pid}, waiting for {delimiter!r} ...")
        self._wait_for_delimiter(startup_timeout)
        _log("ready")

    def _wait_for_delimiter(self, timeout):
        """Block until the subprocess prints the delimiter, with a timeout."""
        q = queue.Queue()

        def _reader():
            buf = b""
            while True:
                ch = self._proc.stdout.read(1)
                if not ch:
                    q.put(EOFError("subprocess exited before printing delimiter"))
                    return
                buf += ch
                if buf.endswith(self._delim):
                    q.put(None)
                    return

        threading.Thread(target=_reader, daemon=True).start()
        try:
            result = q.get(timeout=timeout)
        except queue.Empty:
            self._proc.kill()
            raise TimeoutError(f"subprocess not ready after {timeout}s")
        if isinstance(result, Exception):
            raise result

    def _read_response(self):
        """Read stdout until the next delimiter. Called with _lock held."""
        buf = b""
        while True:
            ch = self._proc.stdout.read(1)
            if not ch:
                raise EOFError("subprocess exited mid-response")
            buf += ch
            if buf.endswith(self._delim):
                return buf[: -len(self._delim)].decode("utf-8", errors="replace")

    def generate(self, prompt):
        """Send prompt, return response text. Blocks; serialises callers."""
        with self._lock:
            if self._proc.poll() is not None:
                raise RuntimeError("subprocess has exited")
            line = (prompt.strip() + "\n").encode()
            self._proc.stdin.write(line)
            self._proc.stdin.flush()
            return self._read_response()

    def stop(self):
        try:
            self._proc.terminate()
        except OSError:
            pass


# ---------------------------------------------------------------------------
# HTTP handler
# ---------------------------------------------------------------------------

def make_handler(cli, model_name, chat_template):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, fmt, *args):
            _log(fmt % args)

        def _json(self, code, obj):
            body = json.dumps(obj).encode()
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self):
            if self.path == "/health":
                self._json(200, {"status": "ok"})
            elif self.path == "/v1/models":
                self._json(200, {
                    "object": "list",
                    "data": [{
                        "id": model_name,
                        "object": "model",
                        "created": int(time.time()),
                        "owned_by": "pipe-wrap",
                    }],
                })
            else:
                self._json(404, {"error": {"message": "not found", "type": "invalid_request_error"}})

        def do_POST(self):
            if self.path != "/v1/chat/completions":
                self._json(404, {"error": {"message": "not found", "type": "invalid_request_error"}})
                return

            length = int(self.headers.get("Content-Length", 0))
            try:
                req = json.loads(self.rfile.read(length))
            except json.JSONDecodeError as e:
                self._json(400, {"error": {"message": str(e), "type": "invalid_request_error"}})
                return

            messages = req.get("messages") or []
            if not messages:
                self._json(400, {"error": {"message": "messages required", "type": "invalid_request_error"}})
                return

            prompt = format_messages(messages, chat_template)
            try:
                text = cli.generate(prompt)
            except Exception as e:
                self._json(503, {"error": {"message": str(e), "type": "server_error"}})
                return

            text = text.strip()
            cid = f"chatcmpl-{uuid.uuid4().hex[:12]}"
            ts = int(time.time())

            if req.get("stream"):
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Cache-Control", "no-cache")
                self.end_headers()
                # Send full text in one chunk, then the stop chunk.
                for delta, finish in [
                    ({"role": "assistant", "content": text}, None),
                    ({}, "stop"),
                ]:
                    chunk = {
                        "id": cid,
                        "object": "chat.completion.chunk",
                        "created": ts,
                        "model": model_name,
                        "choices": [{"index": 0, "delta": delta, "finish_reason": finish}],
                    }
                    try:
                        self.wfile.write(f"data: {json.dumps(chunk)}\n\n".encode())
                        self.wfile.flush()
                    except (BrokenPipeError, ConnectionResetError):
                        return
                try:
                    self.wfile.write(b"data: [DONE]\n\n")
                    self.wfile.flush()
                except (BrokenPipeError, ConnectionResetError):
                    pass
            else:
                self._json(200, {
                    "id": cid,
                    "object": "chat.completion",
                    "created": ts,
                    "model": model_name,
                    "choices": [{
                        "index": 0,
                        "message": {"role": "assistant", "content": text},
                        "finish_reason": "stop",
                    }],
                    "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
                })

    return Handler


# ---------------------------------------------------------------------------
# Helpers / entry point
# ---------------------------------------------------------------------------

def _log(msg):
    print(f"[pipe-wrap] {msg}", file=sys.stderr, flush=True)


def main():
    ap = argparse.ArgumentParser(
        description="HTTP/OpenAI wrapper for stdin/stdout CLI inference tools.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    ap.add_argument("--host", default="0.0.0.0", help="bind address (default: 0.0.0.0)")
    ap.add_argument("--port", type=int, default=8080, help="HTTP port (default: 8080)")
    ap.add_argument(
        "--delimiter", default="\n> ",
        help=r"stdout string that marks end of each response (default: \n> )",
    )
    ap.add_argument("--model-name", default="cli-model", help="name reported in API responses")
    ap.add_argument(
        "--startup-timeout", type=float, default=120.0,
        help="seconds to wait for subprocess to print the delimiter at startup (default: 120)",
    )
    ap.add_argument(
        "--chat-template",
        choices=["last", "simple", "chatml"],
        default="last",
        help=(
            "how to format chat messages into a prompt: "
            "last=last user msg only (default, good for conversation-mode CLIs), "
            "simple=Role: content lines, "
            "chatml=<|im_start|> tokens"
        ),
    )
    ap.add_argument("cmd", nargs=argparse.REMAINDER, help="subprocess command (after --)")
    args = ap.parse_args()

    cmd = args.cmd
    if cmd and cmd[0] == "--":
        cmd = cmd[1:]
    if not cmd:
        ap.error("provide the subprocess command after --")

    try:
        cli = CLIProcess(cmd, args.delimiter, args.startup_timeout)
    except Exception as e:
        _log(f"startup failed: {e}")
        sys.exit(1)

    handler = make_handler(cli, args.model_name, args.chat_template)
    server = ThreadingHTTPServer((args.host, args.port), handler)
    _log(f"listening on http://{args.host}:{args.port}")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        cli.stop()


if __name__ == "__main__":
    main()
