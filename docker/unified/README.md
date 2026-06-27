# Unified Docker Container

These scripts build a self-contained llama-swap image that bundles:

- **llama-server** (llama.cpp) — LLM inference, embeddings, reranking
- **sd-server** (stable-diffusion.cpp) — image generation
- **whisper-server** (whisper.cpp) — speech recognition
- **ik-llama-server** (ik_llama.cpp) — CUDA only

Two backend variants are supported: `cuda` (NVIDIA) and `vulkan` (AMD / Intel).

## Building

```bash
# CUDA (NVIDIA)
./build-image.sh --cuda

# Vulkan (AMD / Intel)
./build-image.sh --vulkan

# Force a clean rebuild
./build-image.sh --cuda --no-cache
```

Each dependency (llama.cpp, whisper.cpp, stable-diffusion.cpp, ik_llama.cpp) is built
from source at the latest upstream HEAD by default.  Pin any of them to a specific
commit, tag, or branch via environment variables:

```bash
LLAMA_REF=b5000   ./build-image.sh --cuda   # pin llama.cpp to a short hash
LLAMA_REF=v1.2.3  ./build-image.sh --cuda   # pin to a release tag
SD_REF=main        ./build-image.sh --vulkan # pin stable-diffusion.cpp to a branch
```

The script resolves the ref to a full 40-character SHA before passing it to Docker,
so the build is fully reproducible and cache-friendly.

## Patching llama.cpp

Sometimes a bug exists in the upstream llama.cpp source that blocks important model
families from loading.  Rather than waiting for a release, patches can be applied to
the llama.cpp source tree at build time before compilation.

### How it works

`install-llama.sh` scans `docker/unified/patches/*.patch` after checking out llama.cpp
and applies each patch with `git apply`.  Before applying, it runs `git apply --check`
to test whether the patch is still needed:

- **Check passes** → patch applied, build continues.
- **Check fails** → the fix was already merged upstream; patch is skipped silently.

This makes patches self-retiring: once the upstream repo ships the same change, the
patch becomes a no-op and can be removed from this directory at any time.

### Adding a patch

1. Reproduce the bug against the relevant llama.cpp commit.
2. Prepare a minimal unified diff rooted at `a/` / `b/` (standard `git diff` output).
3. Add it as `docker/unified/patches/NNNN-short-description.patch`.
4. Open a PR that also links the upstream llama.cpp issue so the patch can be tracked
   and eventually removed.

### Current patches

| File | Affects | Upstream issue |
|------|---------|---------------|
| `0001-qwen35-rope-dimension-sections.patch` | `qwen35.cpp`, `qwen35moe.cpp` | Qwen3.5 GGUFs store 3 entries in `qwen35.rope.dimension_sections`; loaders requested 4, causing a hard load failure. |

## CI / automated builds

The unified image is rebuilt daily by the
[Build Unified Docker Image](.github/workflows/unified-docker.yml) workflow, which
always resolves each dependency to the latest upstream HEAD unless a specific ref is
passed as a `workflow_dispatch` input.  Published images are tagged
`ghcr.io/mostlygeek/llama-swap:unified-{cuda,vulkan}` and
`ghcr.io/mostlygeek/llama-swap:unified-{cuda,vulkan}-YYYY-MM-DD`.
