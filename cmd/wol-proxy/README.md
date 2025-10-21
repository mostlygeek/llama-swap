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
    -timeout 30
```

## API

`GET /status` - that's it. Everything else is proxied to the upstream server.
