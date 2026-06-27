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
and applies each patch using a two-stage check:

1. **Forward check passes** → patch is applied; build aborts if the apply itself fails.
2. **Forward check fails, reverse check passes** → the fix is already in the checked-out
   source (merged upstream); patch is skipped with an explicit "already present" message.
3. **Both checks fail** → the patch is malformed or its context has drifted; build aborts
   with a visible error so the problem is caught at build time, not at model load.

This makes patches self-retiring: once the upstream repo ships the same change, the
patch skips automatically and can then be deleted from this directory.

### Adding a patch

1. Pin your local llama.cpp to the same commit the build will use:
   ```bash
   LLAMA_REF=<commit> ./build-image.sh --cuda --no-cache
   # or clone/checkout manually at the same SHA
   ```
2. Reproduce the bug, make the minimal fix, and produce the diff:
   ```bash
   git diff > docker/unified/patches/NNNN-short-description.patch
   ```
   Use sequential zero-padded numbers for `NNNN` (e.g. `0001`, `0002`).
   Patches are applied in filename sort order, so numbering establishes
   the application order if patches have dependencies.
3. Verify locally: `git apply --check docker/unified/patches/NNNN-short-description.patch`
4. File an issue or PR at [ggml-org/llama.cpp](https://github.com/ggml-org/llama.cpp) for
   the upstream fix and add the link to the table below.
5. Open a PR here. The patch file will be reviewed as C++ source — see `CODEOWNERS`.

When the upstream fix lands, open a follow-up PR to delete the patch file and the table row.

### Current patches

| File | Affects | Track upstream fix |
|------|---------|-------------------|
| `0001-qwen35-rope-dimension-sections.patch` | `qwen35.cpp`, `qwen35moe.cpp` | [ggml-org/llama.cpp](https://github.com/ggml-org/llama.cpp) — issue/PR pending |

## CI / automated builds

The unified image is rebuilt daily by the
[Build Unified Docker Image](.github/workflows/unified-docker.yml) workflow, which
always resolves each dependency to the latest upstream HEAD unless a specific ref is
passed as a `workflow_dispatch` input.  Published images are tagged
`ghcr.io/mostlygeek/llama-swap:unified-{cuda,vulkan}` and
`ghcr.io/mostlygeek/llama-swap:unified-{cuda,vulkan}-YYYY-MM-DD`.
