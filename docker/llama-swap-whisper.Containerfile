ARG WH_IMAGE=ghcr.io/ggml-org/whisper.cpp
ARG WH_TAG=main-cuda
ARG BASE=llama-swap:latest

FROM ${WH_IMAGE}:${WH_TAG} AS ws-source
FROM ${BASE}

ARG UID=10001
ARG GID=10001

COPY --from=ws-source --chown=${UID}:${GID} /app/build/bin/whisper-server /app/whisper/whisper-server
COPY --from=ws-source --chown=${UID}:${GID} /app/build/**/*.so* /app/whisper/
