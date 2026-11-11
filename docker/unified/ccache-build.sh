#!/bin/bash
# Run a project's install script with a compiler cache that outlives the runner.
#
# Usage, from a project Dockerfile:
#   ccache-build.sh run <install command...>
#   ccache-build.sh check
#
# Why. Each project's RUN mounts a buildkit cache at /ccache, but CI creates a
# fresh buildx builder for every job, so that mount starts empty and every
# nightly build compiled ik_llama.cpp's ~430 files from scratch. On a slow
# runner that took longer than the job's time limit and the build was
# cancelled. build-image.sh --stage now publishes the cache as an image after
# each build and binds the previous one at /ccache-seed, so most files are
# cache hits and only what upstream changed is compiled.
#
# `run`:
#   1. logs the CPU count, CPU model and memory, so slow runners can be spotted
#   2. copies the seed's cache into /ccache
#   3. runs the install command, stopped after COMPILE_TIMEOUT_MINUTES if set
#   4. with CCACHE_EXPORT=1, copies /ccache to /ccache-out for the export stage
#   5. records the install command's exit status and exits 0
#
# Exiting 0 when the install failed is what lets a build that ran out of time
# still publish the objects it did compile, so the next run continues from
# there instead of starting over. `check` runs in a later stage and exits with
# the recorded status, so the artifacts image is only built from a complete
# install.
set -euo pipefail

STATUS_FILE=/ccache-build-status
SEED_DIR=/ccache-seed/ccache
EXPORT_DIR=/ccache-out
# Written into every exported cache. The seed falls back to the builder base
# when no cache has been published yet, and this tells the two apart.
MARKER=.llama-swap-ccache

CCACHE_DIR="${CCACHE_DIR:-/ccache}"
export CCACHE_DIR

# A full ik_llama.cpp CUDA build is larger than the base image's 2G default.
# When the cache is too small, ccache evicts files compiled earlier in the same
# build and the next run misses on them.
if [ "${CCACHE_EXPORT:-}" = 1 ]; then
    export CCACHE_MAXSIZE="${CCACHE_EXPORT_MAXSIZE:-6G}"
fi

# Informational only, so nothing in here may fail the build. lscpu rather than
# /proc/cpuinfo: ARM kernels have no "model name" line there.
host_info() {
    local cpus model mem
    cpus="$(nproc 2>/dev/null || echo '?')"
    model="$(lscpu 2>/dev/null | sed -n 's/^Model name: *//p' | head -1 || true)"
    mem="$(awk '/MemTotal/ {printf "%.1f GiB", $2 / 1048576}' /proc/meminfo 2>/dev/null || true)"
    echo "=== Build host: ${cpus} CPUs, ${model:-unknown CPU model}, ${mem:-unknown} RAM ==="
}

case "${1:-}" in
    run)
        shift
        if [ $# -eq 0 ]; then
            echo "usage: ccache-build.sh run <install command...>" >&2
            exit 2
        fi

        host_info || true
        mkdir -p "${CCACHE_DIR}"
        if [ -f "${SEED_DIR}/${MARKER}" ]; then
            echo "=== Seeding ccache from the previous build ($(du -sh "${SEED_DIR}" | cut -f1)) ==="
            cp -a "${SEED_DIR}/." "${CCACHE_DIR}/"
        else
            echo "=== No previous ccache to seed from, compiling cold ==="
        fi
        ccache --zero-stats >/dev/null

        status=0
        start=$(date +%s)
        if [ -n "${COMPILE_TIMEOUT_MINUTES:-}" ] && [ "${COMPILE_TIMEOUT_MINUTES}" != 0 ]; then
            echo "=== Compile time limit: ${COMPILE_TIMEOUT_MINUTES} minutes ==="
            # timeout signals its whole process group, so make and the compilers
            # it started stop too, not just the install script.
            timeout --kill-after=2m "${COMPILE_TIMEOUT_MINUTES}m" "$@" || status=$?
        else
            "$@" || status=$?
        fi
        echo "=== Install finished with status ${status} after $(( ($(date +%s) - start) / 60 )) minutes ==="
        if [ "${status}" = 124 ] || [ "${status}" = 137 ]; then
            echo "ERROR: the compile did not finish within ${COMPILE_TIMEOUT_MINUTES} minutes." >&2
            echo "  The objects it did compile are kept in the published ccache," >&2
            echo "  so the next run continues from where this one stopped." >&2
        fi

        echo "=== ccache statistics for this build ==="
        ccache --show-stats || true

        mkdir -p "${EXPORT_DIR}"
        if [ "${CCACHE_EXPORT:-}" = 1 ]; then
            ccache --cleanup >/dev/null || true
            touch "${CCACHE_DIR}/${MARKER}"
            cp -a "${CCACHE_DIR}/." "${EXPORT_DIR}/"
            echo "=== ccache to publish: $(du -sh "${EXPORT_DIR}" | cut -f1) ==="
        fi

        echo "${status}" > "${STATUS_FILE}"
        ;;
    check)
        status="$(cat "${STATUS_FILE}")"
        if [ "${status}" != 0 ]; then
            echo "ERROR: the install script failed with status ${status}; see the compile step's output." >&2
            exit "${status}"
        fi
        ;;
    *)
        echo "usage: ccache-build.sh run <install command...> | check" >&2
        exit 2
        ;;
esac
