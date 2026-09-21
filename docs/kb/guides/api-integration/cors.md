---
title: CORS and browser access
summary: How llama-swap decides which Access-Control headers to send, and how to allow a specific dashboard origin.
category: guides
tags: [cors, browser, origin, dashboard, litellm, preflight]
config_keys: [security.cors.allowedOrigins, security.cors.allowCredentials, security.cors.allowedMethods, security.cors.allowedHeaders, security.cors.exposedHeaders, security.cors.maxAge]
updated: 2026-09-21
---

# CORS and browser access

## Two modes

`security.cors.allowedOrigins` is the switch. Leave it out and llama-swap
allows any origin, which is what it did before the option existed — so configs
written without it are unaffected.

Set it and you take charge of access: only the origins you list get CORS
headers, and nothing falls back to allow-all.

```yaml
# no allowedOrigins anywhere  -> any origin allowed
```

```yaml
security:
  cors:
    allowedOrigins: ["https://dashboard.example.com"]
# -> only that origin; every other browser origin gets nothing
```

Only `allowedOrigins` works this way. `allowedMethods`, `allowedHeaders` and
`maxAge` describe preflight mechanics rather than access, so each one you leave
out still takes its default and the minimal config above answers a browser's
preflight correctly.

Because those settings are meaningless without origins to apply them to,
setting one without `allowedOrigins` is refused at startup:

```console
error: security.cors: allowedOrigins is required when any other cors setting
is present; remove the security.cors block to keep the permissive default
```

That is deliberate. Quietly falling back to allow-all would widen access for a
config that plainly meant to restrict something.

Two rules apply in both modes:

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

The matching origin is echoed back and `Vary: Origin` is set.

A request from an unlisted origin is still handled normally — llama-swap runs
it and returns the full response, just without CORS headers. The browser then
refuses to hand that response to the page that asked for it. Enforcement is
entirely on the browser side, so `allowedOrigins` is not access control: curl,
scripts and any non-browser client ignore it and get the response. Use
`apiKeys` for that.

For requests that are preflighted (a `POST` with a JSON body, for example) the
browser stops at the failed `OPTIONS` and never sends the real request, so no
model is loaded. A plain cross-origin `GET` has no preflight, so the work
happens and the result is thrown away.

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
