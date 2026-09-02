---
title: Connect llama-swap with Tailcat
summary: Privately expose inference over Tailcat and route peers through Tailcat connection tokens.
category: guides
tags: [tailcat, peers, remote, networking, security]
config_keys: [tailcat, tailcat.allow, tailcat.models, tailcat.admin, tailcat.debug, peers, peers.*.proxy, peers.*.tailcatKey]
updated: 2026-09-02
---

# Connect llama-swap with Tailcat

Tailcat provides end-to-end encrypted connections without a Tailscale account
or control plane. A Tailcat connection token is case-sensitive and acts like a
capability: anyone who has it can attempt to reach the server. Store and share
it as carefully as an API credential.

## Create stable keys

Install the `tailcat` CLI, then create a persistent server identity with a
fixed relay region. Fixing the region keeps its connection token stable across
restarts:

```bash
tailcat genkey --key=/path/to/server.private.json --fixed-region
```

To retrieve the public key for an existing client identity, use the root
`--key` option before `printpub`:

```bash
tailcat --key=/path/to/client.private.json printpub
```

This prints the full `nodekey:...` public key. Put that exact value in the
server allowlist.

Use `tailcat genkey --client --key=/path/to/client.private.json` only when
intentionally creating or rotating a client private key. After a rotation,
update `tailcat.allow` and each affected peer's `tailcatKey` value before using
the new identity.

```yaml
tailcat:
  allow:
    - nodekey:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  models:
    - chat
    - gpu-box/embeddings
  admin: false
  debug: false
```

Start llama-swap with both the configuration and the persistent server key:

```bash
llama-swap -config /path/to/config.yaml \
  -listen-tailcat /path/to/server.private.json
```

`-listen-tailcat` requires a valid Tailcat server PrivateKey JSON file and a
non-empty `tailcat.models` list. Use `models: ["*"]` explicitly to expose all
callable local and peer IDs. An empty `allow` list permits any client holding
the token, though an explicit allowlist is strongly recommended for a
persistent address.

With `admin: false` (the default), Tailcat serves only `/health`, filtered model
listings, and allowlisted model-dispatched inference routes. Set `admin: true`
only when remote callers also need the UI, `/api`, logs, metrics, operations,
or upstream passthrough. The model allowlist still applies in admin mode.

Set `debug: true` to include Tailcat transport diagnostics in the proxy log.
It defaults to `false` to keep routine proxy logs concise.

The listener key, `allow`, and `debug` values are applied at process startup.
Changing them requires restarting llama-swap. Configuration reloads can update
the `models` and `admin` request policy without changing the listener token.

## Find the server token

When the listener starts, llama-swap writes the connection token to its console
log. Copy the value after the colon; it is case-sensitive:

```text
[INFO] Tailcat listening on virtual TCP port 80: tcREPLACE_WITH_CONNECTION_TOKEN
```

You can also open the **Tailcat** page in the llama-swap UI and copy the token
shown under **Tailcat connection token**. The page is available only while the
listener is running.

If llama-swap configures `apiKeys`, Tailcat callers must also send the normal
header, for example `Authorization: Bearer <key>` or `x-api-key: <key>`.
Tailcat node authorization and HTTP API-key authentication are independent.

## Connect a client

Use the token from the console log or Tailcat page. An ephemeral client can run:

```bash
tailcat socks <token> curl http://server.tailcat/v1/models
```

Use the stable client key when the server has an allowlist:

```bash
tailcat --key=/path/to/client.private.json socks <token> curl http://server.tailcat/v1/models
```

Add API-key headers to the curl command when required:

```bash
tailcat --key=/path/to/client.private.json socks <token> \
  curl -H 'Authorization: Bearer <api-key>' http://server.tailcat/v1/models
```

Tailcat uses virtual TCP port 80 for llama-swap; do not add another port to the
connection URL or the `server.tailcat` hostname.

## List models with curl

To list the models exposed by a llama-swap server's Tailcat listener, run curl
through Tailcat's SOCKS wrapper. Replace the token with the exact value shown
on the Tailcat page; it is case-sensitive.

```bash
tailcat socks tcREPLACE_WITH_CONNECTION_TOKEN \
  curl --fail --silent http://server.tailcat/v1/models
```

For a server that uses a caller allowlist and HTTP API keys, use the saved
client key and include the normal llama-swap authentication header:

```bash
tailcat --key=/path/to/client.private.json socks tcREPLACE_WITH_CONNECTION_TOKEN \
  curl --fail --silent \
    -H 'Authorization: Bearer <api-key>' \
    http://server.tailcat/v1/models
```

## Configure a Tailcat peer

Use the destination token verbatim after `tailcat://`. Tokens are
case-sensitive; do not lowercase them.

```yaml
peers:
  gpu-box:
    proxy: tailcat://tcREPLACE_WITH_CONNECTION_TOKEN
    tailcatKey: /path/to/client.private.json
    models: [chat, embeddings]
```

Omit `tailcatKey` (or set it to `ephemeral`) to create one client identity for
that peer for the lifetime of the llama-swap process. A saved client key is
needed when the destination allowlists callers.

Avoid cyclic peer graphs. If host A routes a model to B and B routes that same
request back to A, requests loop until they fail and may repeatedly start or
hold models.
