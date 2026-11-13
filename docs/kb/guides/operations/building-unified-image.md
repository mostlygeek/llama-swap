---
title: Building the unified llama-swap image
summary: How the unified image is assembled from per-project Dockerfiles, how to build it and pin the source revision (LS_VERSION), add an engine, and verify kubeswap is inside.
category: guides
tags: [kubeswap, unified-image, docker, build, llama-server, sd-server, whisper, audiocpp, ls-version, custom-engine]
updated: 2026-11-13
---

# Building the unified llama-swap image

The unified image is the single image every kubeswap model in the examples
points at: one backend server per engine plus the llama-swap head-end and
`kubeswap` itself, all in `/usr/local/bin`. Building your own is the way to
add an engine or match your hardware flavour.

## The three flavours

From `docker/unified/`:

```bash
./build-image.sh --vulkan    # AMD and other Vulkan hardware (amd64)
./build-image.sh --cuda      # NVIDIA Pascal-Ada, CUDA 12 (amd64)
./build-image.sh --cuda13    # NVIDIA Turing-Blackwell, CUDA 13 (amd64, arm64)
```

Pick the flavour your GPU needs; a CPU-only fleet runs any of them (Vulkan
is the leanest). Full build mechanics (caching, CI stages, multi-arch
manifests) are in `docker/unified/README.md`.

## The Go revision: LS_VERSION

The image carries three Go binaries — llama-swap, vllm-wrapper and
kubeswap — all built from **one** llama-swap revision. Set `LS_VERSION` to
the revision you want; it resolves to a commit and is passed as a build arg
to all three builds:

```bash
LS_VERSION=<commit-or-tag> ./build-image.sh --vulkan
```

Without it, the build uses `latest` for llama-swap and a revision pinned in
`runtime.Dockerfile` for kubeswap (pinned because no release ships
`cmd/kubeswap` yet — check the comment in the file). The installer script
refuses a revision without `cmd/kubeswap`, so a mismatch fails the build
rather than silently producing an image without the wrapper.

## Layout: one Dockerfile per piece

| file | produces |
| --- | --- |
| `base-<backend>.Dockerfile` | the builder base (compilers, CUDA/Vulkan SDK) |
| `<project>.Dockerfile` + `install-<project>.sh` | one upstream project, as a `scratch` image of `/install` |
| `runtime.Dockerfile` | the final image, copying those `/install` trees into `/usr/local/bin` |

`runtime.Dockerfile` is the assembly point: each project gets a `FROM
${PROJECT_IMAGE}` stage, and the runtime section copies its whole `/install`
tree into `/usr/local`. A project with no artifacts image falls back to an
empty `/install`, which is how a server is left out (see below). The kubeswap
stage builds from the `LS_VERSION` source in a `golang` container and lands at
`/usr/local/bin/kubeswap`.

### Leaving servers out

By default the image has every server. To build one without some of them, name
the ones you want:

```bash
./build-image.sh --vulkan --projects=llama,whisper
```

The names are `llama`, `whisper`, `sd`, `audio` and `ik-llama`. Servers that
are not named are not compiled or copied into the image, and the build checks
that their binaries are absent. llama-swap, vllm-wrapper and kubeswap are always
included. In GitHub Actions the same choice is the `build_<project>` checkboxes
of a manual run, or the `UNIFIED_DISABLED_PROJECTS` repository variable for the
nightly run. A model whose `cmd` calls a server you left out fails to start.

### Adding an engine

1. Write `<project>.Dockerfile` (compiles the upstream into a `scratch`
   image with `/install/bin/<server>`).
2. Write `install-<project>.sh` (clone/pin/build — the two files are the
   project's complete build inputs; look at `whisper` for the smallest
   example). Put only what should ship under `/install`.
3. In `runtime.Dockerfile`: add an `ARG PROJECT_IMAGE=empty`, a `FROM
   ${PROJECT_IMAGE} AS project-src` stage and a `COPY --from=project-src
   /install/ /usr/local/` line.
4. Register the project in `build-image.sh` (`ALL_PROJECTS`, `project_hash`,
   `project_image_arg`, `project_binaries`) and in the CI workflow (the `all`
   list and a `build_<project>` input in `unified-docker.yml`).

In a model's `cmd`, the new binary is then selected with
`--command <server>` — no other kubeswap concept changes.

## Verifying the image

The image entrypoint runs llama-swap (the head-end), so override it to test
the tools:

```bash
docker run --rm --entrypoint kubeswap <image> version
docker run --rm --entrypoint llama-server <image> --version
docker run --rm --entrypoint sd-server <image> --version
docker run --rm --entrypoint whisper-server <image> --version
```

`kubeswap version` printing your `LS_VERSION` (with the build time you
passed via ldflags) confirms the wrapper is in `/usr/local/bin` at the
revision you asked for — the path the chart and the model `cmd`s expect.
