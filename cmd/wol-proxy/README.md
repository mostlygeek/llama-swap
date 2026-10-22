# wol-proxy

wol-proxy automatically wakes up a suspended llama-swap server using Wake-on-LAN when requests are received.

When a request arrives and llama-swap is unavailable, wol-proxy sends a WOL packet and holds the request until the server becomes available. If the server doesn't respond within the timeout period (default: 60 seconds), the request is dropped.

This utility helps conserve energy by allowing GPU-heavy servers to remain suspended when idle, as they can consume hundreds of watts even when not actively processing requests.

## Usage

```shell
# minimal
$ ./wol-proxy -mac BA:DC:0F:FE:E0:00 -upstream http://192.168.1.13:8080

# everything
$ ./wol-proxy -mac BA:DC:0F:FE:E0:00 -upstream http://192.168.1.13:8080 \
    # use debug log level
    -log debug \
    # altenerative listening port
    -listen localhost:9999 \
    # seconds to hold requests waiting for upstream to be ready
    -timeout 30 \
    # API key sent as Bearer token to the upstream SSE endpoint
    # (can also be set via the LLAMA_SWAP_API_KEY env var; the flag wins if both are set)
    -api-key <key> \
    # do not wake the server automatically, ask for confirmation first
    -require-confirm
```

## Require confirmation

By default any request wakes the server. With `-require-confirm`, nothing is sent until the user agrees:

- Browser requests to `/` and `/ui/` show a page with a "Start server" button. Pressing it sends the WoL packet and shows the loading page until the server is ready.
- All other requests (e.g. API calls) fail immediately with `503 Service Unavailable` and no WoL packet is sent.

## Loading page

While the server wakes up, a loading page shows a progress bar. The proxy remembers in memory how long the last start took, and the bar fills against that time. The bar never shows full before the server is actually ready, and says so if the start takes longer than last time. After a proxy restart, or before the first start, there is no estimate and the bar just loops.

## API

`GET /status` - that's it. Everything else is proxied to the upstream server.
