---
title: Route to remote peers
summary: Expose models from another llama-swap host through a peer connection.
category: guides
tags: [peers, remote, networking]
config_keys: [peers, peers.*.proxy, peers.*.apiKey, peers.*.models]
updated: 2026-08-28
---

# Route to remote peers

Declare a peer with the other llama-swap `proxy` URL and its model list. Its models are addressed as
`peer-name/model-id`, so a peer named `sippy` serving `gemma-4-12B` appears as
`sippy/gemma-4-12B`.

```yaml
peers:
  sippy:
    proxy: http://sippy:8080
    apiKey: ${env.SIPPY_API_KEY}
    models: [gemma-4-12B]
```

The peer must be reachable from this host and its API key must match the remote
server. See `guides/api-integration/api-keys-and-auth` for keeping it out of
the committed file.
