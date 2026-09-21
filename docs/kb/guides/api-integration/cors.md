---
title: CORS and browser access
summary: How llama-swap decides which Access-Control headers to send, and how to allow a specific dashboard origin.
category: guides
tags: [cors, browser, origin, dashboard, litellm, preflight]
config_keys: [security.cors.allowedOrigins, security.cors.allowCredentials, security.cors.allowedMethods, security.cors.allowedHeaders, security.cors.exposedHeaders, security.cors.maxAge]
updated: 2026-09-21
---

# CORS and browser access

## The default

Out of the box llama-swap allows any origin. A browser page on any host can
call `/v1/...`, `/api/...` and `/running`.

Two rules matter more than the defaults:

1. **Headers are only sent when the request carries an `Origin`.** Only browsers
   send that header. curl, litellm and most SDKs do not, so they get a response
   with no `Access-Control-*` header at all.
2. **llama-swap is the only source of these headers.** Any CORS headers set by
   an upstream — `llama-server` and vLLM both set their own — are stripped from
   proxied responses.

## Restricting to one origin

```yaml
security:
  cors:
    allowedOrigins:
      - "https://dashboard.example.com"
      - "http://localhost:5173"
    allowCredentials: true
    maxAge: 600
```

Each origin is a bare `scheme://host[:port]`. A trailing slash or a path would
never match what a browser sends, so llama-swap rejects the config at startup
rather than failing silently later.

When the list does not contain `*`, the matching origin is echoed back and
`Vary: Origin` is set. A request from an origin that is not listed is still
served normally, just without CORS headers — the browser discards the response,
which is the point.

`allowCredentials: true` lets browsers attach cookies and `Authorization`
headers. It cannot be combined with `"*"`: the Fetch spec forbids that pairing,
so llama-swap refuses to start instead of quietly ignoring one of the settings.

## What goes wrong

**`Illegal header value b'*, '` from litellm, or curl reporting HTTP 000.**
This is [issue #85](https://github.com/mostlygeek/llama-swap/issues/85). The
backend sets its own `Access-Control-Allow-Origin`, Go's reverse proxy *adds*
upstream headers rather than replacing them, and the client folds the two into
one invalid value. llama-swap strips the upstream's copies to prevent it. If
you see this again, check whether something else in your chain — a reverse
proxy, an ingress — is adding a second set.

**A browser dashboard gets a CORS error but curl works.** Check that the page's
origin is in `allowedOrigins`, exactly as the browser spells it, including the
port. `http://localhost:5173` and `http://127.0.0.1:5173` are different origins.

**No `Access-Control-*` headers in a curl response.** Expected — curl sends no
`Origin`. Add `-H 'Origin: http://example.com'` to see what a browser gets:

```console
$ curl -sD- -o/dev/null -H 'Origin: http://example.com' \
    http://localhost:8080/running | grep -i access-control
Access-Control-Allow-Origin: *
```

There must be exactly one such line.

## Preflight requests

Browsers send `OPTIONS` before a cross-origin `POST` with a JSON body.
llama-swap answers those itself with `204 No Content` before authentication
runs, because browsers never attach credentials to a preflight — requiring an
API key there would break every cross-origin request.

`allowedHeaders` defaults to echoing the browser's own
`Access-Control-Request-Headers`, after dropping any name that is not a valid
HTTP token. Set it explicitly to pin the list instead.

## Known gap

`Access-Control-Allow-Private-Network` is not sent. Chrome's Private Network
Access handshake can block a page on a public origin from reaching llama-swap
on a LAN address, and no `security.cors` setting changes that today.
