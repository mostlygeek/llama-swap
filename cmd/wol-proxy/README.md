# wol-proxy

wol-proxy automatically wakes up a suspended llama-swap server using Wake-on-LAN when requests are received.

When a request arrives and llama-swap is unavailable, wol-proxy sends a WOL packet and holds the request until the server becomes available. If the server doesn't respond within the timeout period (default: 60 seconds), the request is dropped.

This utility helps conserve energy by allowing GPU-heavy servers to remain suspended when idle, as they can consume hundreds of watts even when not actively processing requests.

## Build

```shell
go build -o wol-proxy ./cmd/wol-proxy
# or: make wol-proxy   (writes to ./build)
```

## Usage

wol-proxy runs on an always-on machine on the same LAN as the llama-swap server. Point clients at wol-proxy instead of llama-swap.

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

## How it works

- wol-proxy keeps an SSE connection open to the upstream's `/api/events`. While it is connected the upstream is "ready". If it fails or closes, the upstream is "not ready" and wol-proxy retries with a growing delay (up to 10 seconds).
- Every 10 seconds it also requests the upstream's `/wol-health`. If that gets no response within 5 seconds (for example the machine suspended without closing the connection), the upstream is marked not ready.
- A request that arrives while the upstream is not ready sends a Wake-on-LAN packet (UDP broadcast to port 9) and waits for the upstream to become ready. Requests to `/api/events` do not wake the server.
- If `llama-swap` uses `apiKeys`, pass `-api-key` (or `LLAMA_SWAP_API_KEY`), otherwise wol-proxy cannot read `/api/events` and never sees the upstream as ready.
- Wake-on-LAN must be enabled on the server's network card (and BIOS), and the packet is a LAN broadcast, so wol-proxy has to be on the same network segment as the server.

## Require confirmation

By default any request wakes the server. With `-require-confirm`, nothing is sent until the user agrees:

- Browser requests to `/` and `/ui/` show a page with a "Start server" button. Pressing it sends the WoL packet and shows the loading page until the server is ready.
- All other requests (e.g. API calls) fail immediately with `503 Service Unavailable` and no WoL packet is sent.

## Loading page

While the server wakes up, a loading page shows a progress bar. The proxy remembers in memory how long the last start took, and the bar fills against that time. The bar never shows full before the server is actually ready, and says so if the start takes longer than last time. After a proxy restart, or before the first start, there is no estimate and the bar just loops.

## API

`GET /status` returns the current upstream status (`ready` or `not ready`) and the number of consecutive connection failures. Everything else is proxied to the upstream server.

See also the knowledge base article `docs/kb/guides/tools/wol-proxy.md`.
