---
title: Routing capacity and request queues
summary: Configure concurrencyLimit and understand queued work while a model is loading or busy.
category: guides
tags: [routing, queue, capacity, concurrency, concurrency-limit, max-concurrent-requests]
config_keys: [routing, models.*.concurrencyLimit]
updated: 2026-08-28
---

# Routing capacity and request queues

There is no `models.*.maxConcurrentRequests` setting. The real per-model key
is `models.*.concurrencyLimit`, which limits active parallel requests.

The router serializes model swaps and queues requests until the selected model
is ready. Avoid using many simultaneous client retries as a capacity control:
they create more queued work. Choose a routing policy that fits the models that
may coexist, then set client timeouts high enough for the queue and load time.

Inspect the Activity view and logs when latency grows. A queue that never drains
usually means the command, proxy, or health check is wrong; see
`guides/model-runtime/troubleshooting-model-wont-load`.
