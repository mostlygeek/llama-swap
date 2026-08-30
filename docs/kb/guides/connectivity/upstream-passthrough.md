---
title: Upstream API passthrough
summary: Control which /upstream model paths are ignored instead of triggering a model swap.
category: guides
tags: [upstream, proxy, api]
config_keys: [upstream]
updated: 2026-08-28
---

# Upstream API passthrough

`upstream` controls the `/upstream/<model>/<path>` passthrough endpoint. Its
`ignorePaths` expressions prevent static or UI paths from triggering a model
swap; it does not configure a fallback OpenAI service for unmatched requests.

Use RE2-compatible expressions and keep the default static-asset patterns
unless a client needs different paths. Check logs to distinguish an ignored
path from a model-routing request.
