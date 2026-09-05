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

| flag | image | platforms | GPUs |
|---|---|---|---|
| `--cuda` | `llama-swap:unified-cuda` | amd64 | NVIDIA Pascal through Ada, on CUDA 12 |
| `--cuda13` | `llama-swap:unified-cuda13` | amd64, arm64 | NVIDIA Ampere through Blackwell, on CUDA 13 |
| `--vulkan` | `llama-swap:unified-vulkan` | amd64 | AMD and other Vulkan hardware |

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

### Platforms

`--platform=linux/amd64|linux/arm64` picks the architecture; it defaults to the
host's, which is what a local build wants. There is no cross-building — the flag
selects which runner CI schedules the job on, and everything compiles natively.
Emulating a multi-hour CUDA compile under QEMU is not practical.

Only `cuda13` is built for arm64, because that is the only place arm64 NVIDIA
hardware exists: GB10, the Blackwell GPU in DGX Spark, sits beside a Grace CPU,
and its `sm_121` needs a CUDA 13 `nvcc`. Every other NVIDIA arm64 part is a Grace
pairing too, so an arm64 CUDA 12 image would have nothing to run on, and no arm64
GPU would be served by the Vulkan image.

The published tags are assembled in two steps. Each platform's image is built,
verified and pushed under an arch-qualified tag (`unified-cuda13-arm64`), and a
manifest job then joins them into the tag users actually pull:

```bash
./build-image.sh --cuda13 --platform=linux/arm64 --assemble --push
./build-image.sh --cuda13 --manifest --platforms=linux/amd64,linux/arm64
```

`docker pull ghcr.io/mostlygeek/llama-swap:unified-cuda13` then resolves to
whichever architecture the host is. Only `--manifest` mints the date-suffixed
tags, so a tag users pull never names one platform's image. If any platform fails
its build, the manifest job does not run: the arch-qualified images that did
succeed are still published, but the shared tag keeps pointing at the last
complete build rather than silently losing an architecture.

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
setup ── resolve every upstream ref once, fix the date tag
  ├─ cuda ─── amd64 ── base ── whisper sd audio llama ik-llama ── assemble ─┬─ manifest
  ├─ cuda13 ┬ amd64 ── base ── whisper sd audio llama ik-llama ── assemble ─┤
  │         └ arm64 ── base ── whisper sd audio llama ik-llama ── assemble ─┼─ manifest
  └─ vulkan ─ amd64 ── base ── whisper sd audio llama ─────────── assemble ─┴─ manifest
```

Platforms are a matrix over the same backend workflow, so a variant's chain is
per-platform and the manifest job waits for every one of them.

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
| `runtime.Dockerfile`, `rootless.Dockerfile`, this README | nothing |
| `install-sd.sh` or `sd.Dockerfile` | sd, all three variants |
| `base-cuda.Dockerfile` | the base and all 5 projects of **both** CUDA variants; no Vulkan |
| `CMAKE_CUDA_ARCHITECTURES` or `CUDA_VERSION` env var | the base and all 5 projects of the one variant being built, and its runtime image |
| the built-in `CMAKE_CUDA_ARCHITECTURES`/`CUDA_VERSION` default for one variant, in `build-image.sh` | that variant only, and only the platform whose default changed |
| `base-vulkan.Dockerfile` | the Vulkan base and all 4 Vulkan projects; no CUDA |

and means a project that overruns its own job no longer discards the ones that
finished, reruns are idempotent, and a failed assemble can be retried without
recompiling anything.

### Driving it by hand

```bash
./build-image.sh --cuda --resolve         # print resolved commit hashes
./build-image.sh --cuda --stage=base      # build + push the builder base
./build-image.sh --cuda --stage=llama     # build + push one project's artifacts
./build-image.sh --cuda --assemble        # assemble one platform from published images
./build-image.sh --cuda --manifest        # join the platforms into one tag
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

| image | platform | CUDA | `CMAKE_CUDA_ARCHITECTURES` | covers |
|---|---|---|---|---|
| `unified-cuda` | amd64 | 12.9.1 | `60;61;75;86;89` | Pascal (P100, GTX 10xx, P40), Turing, Ampere, Ada |
| `unified-cuda13` | amd64 | 13.3.1 | `80;86;89;90;100;120` | Ampere (A100, RTX 30xx), Ada (RTX 40xx), Hopper (H100), Blackwell (100 on datacenter parts, 120 on GeForce RTX 50xx and RTX PRO) |
| `unified-cuda13` | arm64 | 13.3.1 | `90;100;120;121` | GB10 (DGX Spark) at 121, GH200 at 90, GB200 at 100, and a discrete GeForce or RTX PRO card in an aarch64 host at 120 |

Those are the compute capabilities compiled as SASS. For most entries CMake also
emits PTX, so an architecture above one of them still runs by JIT-compiling that
PTX — it pays that cost on first load and misses arch-specific kernels. On
`unified-cuda` that covers Volta (70), Ampere (80) and Hopper (90).

Blackwell is the exception, in two ways that both matter when editing these
lists.

**`120` is not what actually gets compiled.** ggml's CUDA `CMakeLists.txt`
rewrites any plain `12X` into the architecture-specific `12Xa`, because
Blackwell's FP4 tensor core instructions are not forwards compatible and exist
only under that target. Writing `120` is what you want: it is how an RTX 5090 or
RTX PRO 6000 Blackwell (both GB202, sm_120) gets native FP4-capable code, without
naming `120a` yourself — and naming it yourself would risk `ik_llama.cpp`, an
older fork with no such rewrite, which compiles the plain number. The catch is
that `12Xa` targets are real-only: they emit no PTX, so **nothing in the 12.x
family is reachable by JIT**. A 12.x GPU runs only if its number is listed.

**Blackwell spans two CUDA major versions.** Datacenter parts are compute 10.x
(`100`, `103`); GeForce, RTX PRO and GB10 are 12.x (`120`, `121`). PTX only JITs
forward within a major version, so neither branch can stand in for the other —
dropping `100` does not leave GB200 covered by `120`.

`121` is deliberately absent from the amd64 list. GB10 is a Grace SoC with no
PCIe part, so sm_121 cannot appear in an x86 host; it is listed on arm64, where
it is the whole point.

Pick `unified-cuda13` for an Ampere or newer card and `unified-cuda` for
anything older. `unified-cuda13` is a multi-arch tag, so on a DGX Spark or any
other aarch64 host `docker pull` resolves to the arm64 image without asking. What
an image is and what it was compiled for is recorded inside it:

```bash
docker run --rm --entrypoint cat ghcr.io/mostlygeek/llama-swap:unified-cuda13 /versions.txt
```

To compile an architecture natively that a variant does not list — Jetson's `87`
or Thor's `110`, say, both of which want an l4t base image rather than
`nvidia/cuda` — set `CMAKE_CUDA_ARCHITECTURES` when invoking `build-image.sh`; to
change a default, edit the `case` on `VARIANT` and `ARCH` near the top of that
script. The base is what every CUDA project compiles from, so an addition
lengthens all of them — and changes the base's hash, which rebuilds all five
projects of that variant on that platform.

The Vulkan image builds audio.cpp with `ENGINE_ENABLE_VULKAN=ON`. audio.cpp is
tuned for CUDA, and the server prints a notice on startup that a non-CUDA
backend may have lower performance and model coverage.
