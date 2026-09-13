---
title: Use the Playground over Tailcat from a browser
summary: Build and use a standalone HTML page that reaches a Tailcat-exposed llama-swap node from any browser.
category: guides
tags: [tailcat, playground, ui, remote, browser, wasm]
config_keys: [tailcat, tailcat.allow, tailcat.models, tailcat.admin, apiKeys]
updated: 2026-09-09
---

# Use the Playground over Tailcat from a browser

A node started with `-listen-tailcat` is reachable over Tailcat, but not by an
ordinary browser: Tailcat is WireGuard relayed over DERP, which `fetch` does not
speak. The built-in UI at `/ui/` is also blocked unless `tailcat.admin` is true.

The Tailcat Playground page solves this by carrying a Tailcat client of its own,
compiled to WebAssembly. Open the page, give it a connection token, and you get
the normal Playground &mdash; chat, images, speech, transcription, rerank and the
load test &mdash; talking to the node through the tunnel. Only your browser and
the node see the traffic; whatever is hosting the page does not.

It works against the default `tailcat.admin: false`, because everything the
Playground calls (`/v1/models` and the model-dispatched inference routes) is
already in Tailcat's narrow capability surface.

## Build the page

```bash
make tailcat-playground
```

This writes two packagings of the same page to `build/tailcat-playground/`:

| File | Use |
| --- | --- |
| `llama-swap-tailcat-playground.html` | One self-contained file, about 10MB. Copy it anywhere and open it, including from `file://`. |
| `index.html` + `main.wasm.gz` | The same page with the module beside it instead of inside it. Serve both from any static web server. It does not work from `file://`: browsers refuse to fetch a sibling file there, so use the single file for that. |
| `tailcat-playground-server` | A binary for the machine you built on, with the split pair embedded. Run it and open the address it prints; `-listen` changes it from the default `127.0.0.1:8090`. |

The WebAssembly module is the whole Tailscale data plane, so the first build
takes about a minute. Nothing embeds the page in the llama-swap binary.

## Connect

Start the node as usual (see [Connect llama-swap with
Tailcat](tailcat.md)) and copy the connection token from its log:

```text
[INFO] Tailcat listening on virtual TCP port 80: tcREPLACE_WITH_CONNECTION_TOKEN
```

Open the page, choose **Add a server**, and fill in:

- **Name** &mdash; whatever you want to call the node. Defaults to a prefix of
  the token.
- **Connection token** &mdash; the `tc...` value, exactly as printed. Tokens are
  case-sensitive.
- **API key** &mdash; only when the node configures `apiKeys`. Tailcat node
  authorization and HTTP API-key authentication are independent, so a node can
  require both.
- **DERP map URL** &mdash; leave empty unless you run your own relays.

**Save and connect** stores the server and connects to it. **Save** just stores
it, so you can add several before connecting to any of them.

## Manage several nodes

The page keeps as many servers as you add. Each row in the list has **Connect**,
an edit button and a delete button, so a token that rotates or an API key that
changes is an edit rather than a re-entry.

Connecting to a server makes it the active one. It is marked **Last used** and
kept at the top of the list, and the page reopens on it, so switching between a
workstation and a remote box is two clicks rather than a token paste. Rows also
show an **API key** badge when one is set, which is the quickest way to see why
a node is answering 401.

**Disconnect** in the header returns to the list without dropping anything you
have saved.

## Allowlisted nodes

When the node sets `tailcat.allow`, only the listed client keys may connect. The
page generates its own client key on first load and shows it under **This
browser's node key**:

```yaml
tailcat:
  allow:
    - nodekey:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  models:
    - chat
```

The key is stored in the browser and stays the same across reloads, so it only
has to be added once. `tailcat.allow` is read at startup, so restart llama-swap
after changing it. Clearing the browser's storage generates a new key, and the
page says so when that has happened.

## What is stored, and where

Everything the page remembers lives in the browser's local storage: the server
list, which one was last used, and this browser's Tailcat client key. It never
leaves the browser, which also means it is per-browser and per-profile &mdash;
another machine starts with an empty list and a different node key.

The connection token and API key are held there unencrypted. Both are bearer
credentials: anyone holding the token can attempt to reach the node. Treat the
machine you save them on the way you would treat a file containing an API key,
and delete the server entry when you are done with it.

## Notes

- The page fetches a DERP map from `https://tailcat.dev/derpmap.json` before it
  can connect, so the browser needs to reach that once even though nothing
  about the session goes through it.
- The model list is fetched on connect rather than kept live, because
  `/api/events` is not part of Tailcat's non-admin surface. Use **Refresh
  models** after loading or unloading models on the node.
- Only models listed in `tailcat.models` appear, which is the same filtering any
  other Tailcat caller sees.
- The Load Test tab drives its concurrency through one tunnel, so its numbers
  describe the relay as much as the model.
