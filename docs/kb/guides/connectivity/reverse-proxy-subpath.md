---
title: Serving llama-swap under a reverse proxy subpath
summary: Run llama-swap at https://host/some/path/ by having the proxy strip the prefix and rewriting it away.
category: guides
tags: [reverse-proxy, base-path, subpath, nginx, caddy, prefix, ui]
updated: 2026-11-14
---

# Serving llama-swap under a reverse proxy subpath

llama-swap serves everything from its own root (`/ui`, `/v1`, `/api`, ...) and
has no base-path setting. To reach it at `https://host/asubpath/llama-swap/`,
configure the proxy to **strip** the prefix before forwarding. The UI requests
its assets and API endpoints relative to the page, so it follows the prefix
automatically.

## nginx

```nginx
location /asubpath/llama-swap/ {
    proxy_pass http://127.0.0.1:8080/;   # trailing slash strips the prefix
    proxy_read_timeout 3600s;            # streams are long-lived
}
```

The trailing slash on `proxy_pass` is what does the rewrite; without it
llama-swap receives `/asubpath/llama-swap/...` and answers 404.

## Caddy

```caddy
handle_path /asubpath/llama-swap/* {
    reverse_proxy localhost:8080
}
```

`handle_path` strips the matched prefix; plain `handle` does not.

## What breaks without stripping

If the prefix is forwarded instead of stripped, llama-swap answers 404 for
everything, because it only registers routes at its own root. Do not "fix"
this by routing `/ui` and `/api` at the proxy root too; the prefix must be
stripped on the way in and re-added by the browser on the way back, which is
exactly what a strip rewrite does.

Also always include the trailing slash in client URLs
(`https://host/asubpath/llama-swap/`, not `.../llama-swap`): redirects on
server responses are emitted relative to that URL, and a missing slash lets
the browser resolve them one directory up.
