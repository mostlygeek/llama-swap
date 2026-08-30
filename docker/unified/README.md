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

For local builds, one command compiles everything and assembles the image:

```bash
./build-image.sh --cuda      # or --vulkan
```

### How CI builds it

Compiling every project in a single job put five concurrent CUDA builds on a
four-core runner and stopped fitting in the 6h GitHub Actions job limit. Worse,
a cancelled job never reaches its cache export, so nothing was cached and the
next night rebuilt everything again — one overrun kept every later run failing.

`unified-docker.yml` splits that into a job per project. Each one compiles a
single upstream project and pushes an artifacts image holding nothing but that
project's `/install` tree:

```
setup ── resolve every upstream ref once
  ├─ build whisper / sd / audio / llama / ik-llama   (one runner each)
  └─ assemble ── COPY the artifacts into the runtime image, verify, push
```

The image is the same either way. Every project reaches the runtime stage
through `COPY --from=<project>-src /install/...`, and `<project>-src` resolves
to a locally compiled stage by default or to a published artifacts image when
`<PROJECT>_IMAGE` is set. BuildKit only walks the branch it needs, so an
assemble build never enters a compile stage.

Artifacts images are tagged with the upstream commit they were built from
(`:art-llama-cuda-<commit>`), which means:

- a project whose upstream has not moved is already published, and its job
  exits without building
- a project that overruns its own job no longer discards the four that finished
- reruns are idempotent, and a failed assemble can be retried without
  recompiling anything

The scripted equivalents, should you need to drive it by hand:

```bash
./build-image.sh --cuda --resolve         # print resolved commit hashes
./build-image.sh --cuda --stage=llama     # build + push one project's artifacts
./build-image.sh --cuda --assemble        # assemble from published artifacts
```

`--stage` and `--assemble` push to and read from `ARTIFACT_REPO` (default
`ghcr.io/mostlygeek/llama-swap`), so they need registry credentials and a
buildx container driver. They are meant for CI; use the plain
`./build-image.sh --cuda` above for local work and under `act`.

Because artifacts images are addressed by content, the old
`:unified-<backend>-cache` BuildKit cache tags are no longer written and can be
deleted from the registry.

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
number to `CMAKE_CUDA_ARCHITECTURES` in the Dockerfile to compile one of them
natively; the list is shared with llama.cpp, whisper.cpp, stable-diffusion.cpp
and ik_llama.cpp, so each addition lengthens every build.

The Vulkan image builds audio.cpp with `ENGINE_ENABLE_VULKAN=ON`. audio.cpp is
tuned for CUDA, and the server prints a notice on startup that a non-CUDA
backend may have lower performance and model coverage.
