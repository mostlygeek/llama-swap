---
title: Client compatibility and loading feedback
summary: Let clients discover models and handle loading responses instead of treating a swap as a failure.
category: guides
tags: [clients, loading, models, api]
config_keys: [includeAliasesInList, sendLoadingState, models.*.aliases]
updated: 2026-11-18
---

# Client compatibility and loading feedback

Clients should read `/v1/models` before choosing a model and be prepared for a
request to wait while llama-swap loads it. Set `sendLoadingState: true` when a
client can display a loading response rather than timing out or retrying
blindly. `includeAliasesInList` also exposes configured aliases in model lists.

```yaml
sendLoadingState: true
includeAliasesInList: true
```

This changes feedback, not loading speed. Keep the client timeout longer than
the model's startup time and use a client that understands the selected API.

Aliases must differ from their own model ID and be unique across the config.
For example:

```yaml
models:
  local-chat:
    proxy: http://127.0.0.1:9999
    aliases: [chat]
```

An alias of `local-chat` on that same model causes config loading to fail.
Remove the redundant alias; clients can already use the model ID directly.
