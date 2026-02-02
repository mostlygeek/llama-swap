ARG SD_IMAGE=ghcr.io/leejet/stable-diffusion.cpp
ARG SD_TAG=master-vulkan
ARG BASE=llama-swap:latest

FROM ${SD_IMAGE}:${SD_TAG} AS sd-source
FROM ${BASE}

ARG UID=10001
ARG GID=10001

COPY --from=sd-source --chown=${UID}:${GID} /sd-server /app/sd-server
