---
title: Running models in Docker or Podman containers
summary: Start a model's inference server as a Docker or Podman container, including from the unified llama-swap image, which ships the docker and podman clients.
category: guides
tags: [docker, podman, container, cmd-stop, socket, unified, gpu, volumes, proxy]
config_keys: [models.*.cmd, models.*.cmdStop, models.*.proxy, models.*.unloadTimeout]
updated: 2026-11-03
---

# Running models in Docker or Podman containers

A model's `cmd` can be a `docker run` or `podman run` command. llama-swap
starts it, waits for the health check, and stops the container on unload.

## Config

```yaml
models:
  qwen-docker:
    proxy: "http://127.0.0.1:${PORT}"
    cmd: |
      docker run --name ${MODEL_ID} --init --rm
      --gpus all
      -p ${PORT}:8080
      -v /mnt/nvme/models:/models
      ghcr.io/ggml-org/llama.cpp:server-cuda
      --model /models/qwen3-8b.gguf
    cmdStop: docker stop ${MODEL_ID}
    unloadTimeout: 30
```

For Podman, change the engine and the GPU flag (CDI, needs the NVIDIA
container toolkit set up on the host):

```yaml
    cmd: |
      podman run --name ${MODEL_ID} --rm
      --device nvidia.com/gpu=all
      -p ${PORT}:8080
      -v /mnt/nvme/models:/models
      ghcr.io/ggml-org/llama.cpp:server-cuda
      --model /models/qwen3-8b.gguf
    cmdStop: podman stop ${MODEL_ID}
```

- `--name ${MODEL_ID}` gives `cmdStop` something to refer to.
- `-p ${PORT}:8080` publishes the container's port on the host port that
  llama-swap picked. Set `proxy` to match.
- Always set `cmdStop`. Without it, an unload stops only the local `docker run`
  client and can leave the container running and holding VRAM.
- Raise `unloadTimeout`; stopping a container takes longer than the default.

## From the unified llama-swap image

The `unified-*` images include the `docker` and `podman` command-line clients
but no engine. They talk to the engine on the host through a socket you mount.
The model containers run on the host, next to llama-swap, not inside it.

Docker:

```yaml
services:
  llama-swap:
    image: ghcr.io/mostlygeek/llama-swap:unified-cuda13
    network_mode: host
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./config.yaml:/etc/llama-swap/config/config.yaml
```

Podman (the `podman` command in the image is the remote client):

```yaml
services:
  llama-swap:
    image: ghcr.io/mostlygeek/llama-swap:unified-cuda13
    network_mode: host
    volumes:
      - /run/podman/podman.sock:/run/podman/podman.sock
      - ./config.yaml:/etc/llama-swap/config/config.yaml
```

Start the Podman socket on the host first: `systemctl enable --now
podman.socket`. For a rootless Podman socket, mount
`$XDG_RUNTIME_DIR/podman/podman.sock` and set
`CONTAINER_HOST=unix:///run/podman/podman.sock` on the llama-swap container.

## What goes wrong

- **Volume paths are host paths.** The host's engine resolves `-v
  /mnt/nvme/models:/models`, so the left side must exist on the host. A path
  that exists only inside the llama-swap container gives an empty directory.
- **Requests hang or fail with connection errors.** llama-swap cannot reach
  the published port. Use `network_mode: host`, or without it point `proxy`
  at the host (for example `http://host.docker.internal:${PORT}` with
  `--add-host=host.docker.internal:host-gateway` on the llama-swap container).
- **`permission denied` on the socket.** The non-root image runs as UID 10001.
  Add the group that owns the socket, for example `group_add: ["<docker gid>"]`.
- **The model keeps running after an unload.** `cmdStop` is missing, or
  `unloadTimeout` is too short and llama-swap killed the client early.
- **A `podman run` flag is rejected.** The image's `podman` is Ubuntu's 4.9.3
  remote client. If the host runs a much newer Podman, check this version
  first.

Access to the container engine socket is effectively root on the host. See
`guides/operations/container-security`.

## Related

- `guides/model-runtime/writing-cmd` — `cmd`, `proxy` and `${PORT}`
- `guides/model-runtime/ttl-and-unloading` — `cmdStop` and `unloadTimeout`
