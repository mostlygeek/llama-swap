---
title: Proxy and model startup timeouts
summary: Distinguish client proxy timeouts from health-check and model startup failures.
category: guides
tags: [proxy, timeout, health-check, loading]
config_keys: [healthCheckTimeout, models.*.proxy, models.*.checkEndpoint]
updated: 2026-08-28
---

# Proxy and model startup timeouts

`healthCheckTimeout` limits how long llama-swap waits for a newly started model
to become healthy. `proxy` must point to the model server that llama-swap can
reach, and `checkEndpoint` must return success there.

If a request times out after the process starts, verify the proxy URL and
endpoint from the llama-swap host first. Raising a timeout only hides a wrong
port, Docker mapping, or unavailable health endpoint.
