# Unified Docker Container

These scripts create a custom llama-swap container that contains:

- llama-server for LLMs, rerank and embedding model support
- sd-server (stable-diffusion.cpp) for image generation
- whisper.cpp for ASR
- audiocpp_server (audio.cpp) for TTS and audio tasks (`/audioapi/v1/tasks/run`)
- vllm-wrapper for vLLM sleep mode support (see [cmd/vllm-wrapper](../../cmd/vllm-wrapper/README.md))

`vllm-wrapper` is built from the same llama-swap revision as the `llama-swap`
binary in the image. It expects a vLLM server started with `--enable-sleep-mode`
that is reachable from the container; vLLM itself is not included in the image.

## Building

```bash
./build-image.sh --cuda      # or --vulkan
```

That compiles everything and assembles the image, same as before.

### Layout

The build is one Dockerfile per piece:

| file | produces |
|---|---|
| `base-<backend>.Dockerfile` | the builder base (compilers, CUDA/Vulkan SDK) |
| `<project>.Dockerfile` | one upstream project, as a `scratch` image of `/install` |
| `runtime.Dockerfile` | the final image, copying those `/install` trees in |

A project's build inputs are therefore exactly two files — its own Dockerfile
and its install script — plus the tag of the base it compiles from. Nothing has
to be inferred from a shared file.

### How CI builds it

Compiling every project in one job put five concurrent CUDA builds on a
four-core runner and stopped fitting in the 6h GitHub Actions job limit. Worse,
a cancelled job never reaches its cache export, so nothing was cached and the
next night rebuilt everything again — one overrun kept every later run failing.

`unified-docker.yml` gives each piece its own job and its own 6h budget:

```
setup ── resolve every upstream ref once
  ├─ cuda ─── base ── whisper sd audio llama ik-llama ── assemble ── push
  └─ vulkan ─ base ── whisper sd audio llama ─────────── assemble ── push
```

Each backend is a separate call to `unified-docker-backend.yml`, which holds
the base → projects → assemble chain for one backend. They are separate calls
rather than one matrix because `needs` applies to a whole job, not to
individual matrix cells: sharing a job graph would keep the Vulkan image, whose
four projects finish in minutes, waiting on CUDA's multi-hour ik_llama.cpp
compile before it could publish.

Every image is addressed by its content, so anything unchanged is skipped:

- the base by its own Dockerfile's hash
- a project by its upstream commit, plus a hash of its Dockerfile, its install
  script, the base tag and the build args it reads

Which gives, concretely:

| edit | rebuilds |
|---|---|
| `runtime.Dockerfile`, this README | nothing |
| `install-sd.sh` or `sd.Dockerfile` | sd, both backends |
| `base-cuda.Dockerfile` (e.g. `CMAKE_CUDA_ARCHITECTURES`) | the CUDA base and all 5 CUDA projects; no Vulkan |
| `base-vulkan.Dockerfile` | the Vulkan base and all 4 Vulkan projects; no CUDA |

and means a project that overruns its own job no longer discards the ones that
finished, reruns are idempotent, and a failed assemble can be retried without
recompiling anything.

### Driving it by hand

```bash
./build-image.sh --cuda --resolve         # print resolved commit hashes
./build-image.sh --cuda --stage=base      # build + push the builder base
./build-image.sh --cuda --stage=llama     # build + push one project's artifacts
./build-image.sh --cuda --assemble        # assemble from published images
```

`--stage` and `--assemble` push to and read from `ARTIFACT_REPO` (default
`ghcr.io/mostlygeek/llama-swap-build`), so they need registry credentials and a
buildx container driver.

That is a separate GHCR package from the published `llama-swap` images on
purpose. Artifacts are build inputs rather than releases — a new tag per project
per upstream commit, most nights — so keeping them in the release package would
bury `:unified-cuda` under thousands of `:art-*` tags. It also keeps them
clear of the `delete-untagged` cleanup in `containers.yml`, which is scoped to
`package: llama-swap`, so the two never interact and the build package can be
given its own retention policy. They are for CI; use plain `./build-image.sh --cuda` locally
and under `act`. The local path chains the images through the docker image
store, so it wants buildx's default `docker` driver (the default) rather than a
container driver.

Because images are addressed by content, the old `:unified-<backend>-cache`
BuildKit cache tags are no longer written and can be deleted from the registry.

## audio.cpp

`audiocpp_server` needs its own JSON config listing the models it serves. The
image ships a starter with the backend it was built for already set:

```bash
docker run --rm --entrypoint cat llama-swap:unified-cuda \
  /etc/llama-swap/audiocpp-server.example.json > /path/to/models/audiocpp-server.json
```

Replace the example entries with your models, then point the `audio` entry in
`config.yaml` at it (see `docs/config.example.yaml`). Every model needs a `family`
matching an audio.cpp model spec, and a `path` to the package inside the
container.

The `backend` field must match the image: `cuda` or `vulkan`. `audiocpp_server`
defaults to `cuda` regardless of how it was compiled, so a vulkan image with an
unset backend fails to load models.

audio.cpp is compiled as a **deployment build**
(`AUDIOCPP_DEPLOYMENT_BUILD=ON`), which compiles the `model_specs/*.json`
catalog into the binaries. Without it the runtime can only use a spec embedded
in a GGUF or found in a `model_specs/` directory near the working directory,
neither of which a container of bare binaries has. The catalog is also
installed at `/usr/local/share/audiocpp/model_specs` for `--model-spec-override`
when a spec needs to be edited or pinned:

```yaml
cmd: |
  audiocpp_server
  --config /models/audiocpp-server.json
  --model-spec-override /usr/local/share/audiocpp/model_specs
  --port ${PORT}
```

### GPU support

The CUDA image compiles SASS for compute capabilities `60;61;75;86;89`, so
Pascal (P100, GTX 10xx, P40) and newer NVIDIA GPUs are supported. Architectures
between and above those entries — Volta (70), Ampere (80), Hopper (90) and
Blackwell (100 on datacenter parts, 120 on GeForce and RTX PRO) — run by
JIT-compiling the nearest lower PTX, which costs time on first load. Add the
number to `CMAKE_CUDA_ARCHITECTURES` in `base-cuda.Dockerfile` to compile one of
them natively. The base is what every CUDA project compiles from, so an addition
lengthens all of them — and changes the base's hash, which rebuilds all five.

The Vulkan image builds audio.cpp with `ENGINE_ENABLE_VULKAN=ON`. audio.cpp is
tuned for CUDA, and the server prints a notice on startup that a non-CUDA
backend may have lower performance and model coverage.
