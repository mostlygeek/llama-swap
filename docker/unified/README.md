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
./build-image.sh --cuda      # or --cuda13, or --vulkan
```

That compiles everything and assembles the image, same as before.

Three images are built, and `--cuda`/`--cuda13`/`--vulkan` picks which:

| flag | image | GPUs |
|---|---|---|
| `--cuda` | `llama-swap:unified-cuda` | NVIDIA Pascal through Ada, on CUDA 12 |
| `--cuda13` | `llama-swap:unified-cuda13` | NVIDIA Ampere through Blackwell, on CUDA 13 |
| `--vulkan` | `llama-swap:unified-vulkan` | AMD and other Vulkan hardware |

`cuda` and `cuda13` are the same CUDA build: same Dockerfiles, same install
scripts, `BACKEND=cuda` in both. Only `CUDA_VERSION` and
`CMAKE_CUDA_ARCHITECTURES` differ, and the builder base tag is keyed on both,
so each gets its own base and artifacts images and neither invalidates the
other. The script calls that published flavour the **variant**, to keep it
distinct from the backend that decides how things are compiled.

One caveat for local builds: the BuildKit cache mounts holding ccache and the
CMake build directories are keyed on the backend, so the two CUDA variants share
them. Building one right after the other on the same machine reconfigures and
recompiles from scratch — correct, because every install script clears
`CMakeCache.txt` before configuring, but not incremental. CI is unaffected: each
variant's jobs get their own runners and start cold anyway.

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
  ├─ cuda13 ─ base ── whisper sd audio llama ik-llama ── assemble ── push
  └─ vulkan ─ base ── whisper sd audio llama ─────────── assemble ── push
```

Each variant is a separate call to `unified-docker-backend.yml`, which holds
the base → projects → assemble chain for one variant. They are separate calls
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
| `install-sd.sh` or `sd.Dockerfile` | sd, all three variants |
| `base-cuda.Dockerfile` | the base and all 5 projects of **both** CUDA variants; no Vulkan |
| `CMAKE_CUDA_ARCHITECTURES` or `CUDA_VERSION` env var | the base and all 5 projects of the one variant being built, and its runtime image |
| the built-in `CMAKE_CUDA_ARCHITECTURES`/`CUDA_VERSION` default for one variant, in `build-image.sh` | that variant only; the other CUDA variant and Vulkan are untouched |
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

Substitute `--cuda13` or `--vulkan` for `--cuda` to drive another variant; the
tags carry the variant name, so `--stage=llama` publishes
`:art-llama-cuda13-<commit>-<recipe>` and never collides with the CUDA 12 one.

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
unset backend fails to load models. The `unified-cuda13` image is a CUDA build,
so its field is `cuda` too — the starter it ships already has it set.

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

### CUDA version

The CUDA toolkit the projects compile against and the runtime libraries the
final image ships both come from `nvidia/cuda` images, pinned to the same
version, so compiled binaries and their runtime libraries always come from the
same CUDA. The default depends on the variant: `12.9.1` for `--cuda` and
`13.3.1` for `--cuda13`.

Set the `CUDA_VERSION` environment variable to use another one — it takes the
version portion of the `nvidia/cuda` image tag, so `CUDA_VERSION=12.6.0` builds
from `nvidia/cuda:12.6.0-devel-ubuntu24.04`. It changes the CUDA base's hash, so
a new version rebuilds the base, all five CUDA projects, and the runtime image
for that variant.

### GPU support

Two CUDA images are built, because CUDA 13 removed Maxwell, Pascal and Volta
from `nvcc` altogether and those cards still need an image:

| image | CUDA | `CMAKE_CUDA_ARCHITECTURES` | covers |
|---|---|---|---|
| `unified-cuda` | 12.9.1 | `60;61;75;86;89` | Pascal (P100, GTX 10xx, P40), Turing, Ampere, Ada |
| `unified-cuda13` | 13.3.1 | `80;86;89;90;100;120` | Ampere (A100, RTX 30xx), Ada (RTX 40xx), Hopper (H100), Blackwell (100 on datacenter parts, 120 on GeForce RTX 50xx and RTX PRO) |

Those are the compute capabilities compiled as SASS. CMake emits PTX alongside
SASS for every entry, so an architecture between or above the list still runs by
JIT-compiling the nearest lower PTX — it just pays that cost on first load and
misses arch-specific kernels. On `unified-cuda` that covers Volta (70), Ampere
(80), Hopper (90) and Blackwell; on `unified-cuda13`, anything above `120`.

Pick `unified-cuda13` for an Ampere or newer card and `unified-cuda` for
anything older. Which one an image is, and what it was compiled for, is recorded
in the image:

```bash
docker run --rm --entrypoint cat ghcr.io/mostlygeek/llama-swap:unified-cuda13 /versions.txt
```

To compile an architecture natively that a variant does not list — Jetson's
`87`, say, or a future Blackwell derivative — set `CMAKE_CUDA_ARCHITECTURES`
when invoking `build-image.sh`; to change a variant's default, edit the `case`
on `VARIANT` near the top of `build-image.sh`. The base is what every CUDA
project compiles from, so an addition lengthens all of them — and changes the
base's hash, which rebuilds all five projects of that variant.

The Vulkan image builds audio.cpp with `ENGINE_ENABLE_VULKAN=ON`. audio.cpp is
tuned for CUDA, and the server prints a notice on startup that a non-CUDA
backend may have lower performance and model coverage.
