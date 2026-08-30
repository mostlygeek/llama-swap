---
title: Startup preloading and hooks
summary: Load selected models at startup and run hooks around lifecycle events.
category: guides
tags: [startup, preload, hooks, operations]
config_keys: [hooks, models.*.ttl]
updated: 2026-08-28
---

# Startup preloading and hooks

Use startup hooks when an external action must run as llama-swap starts, and
preload only models your machine can keep resident. A model with `ttl: 0` stays
loaded after it has been requested; preloading does not make an incompatible
model fit in memory.

Keep hook commands fast and observable. A failing hook or a model that cannot
become healthy should be diagnosed in logs before adding retries.
