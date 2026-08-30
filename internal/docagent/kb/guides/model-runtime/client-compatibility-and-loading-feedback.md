---
title: Client compatibility and loading feedback
summary: Let clients discover models and handle loading responses instead of treating a swap as a failure.
category: guides
tags: [clients, loading, models, api]
config_keys: [includeAliasesInList, sendLoadingState]
updated: 2026-08-28
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
