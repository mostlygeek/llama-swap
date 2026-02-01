ARG BASE=llama-swap:latest
FROM ${BASE}

ARG SD_IMAGE=ghcr.io/leejet/stable-diffusion.cpp
ARG SD_TAG=master-cuda
ARG UID=10001
ARG GID=10001

COPY --from=${SD_IMAGE}:${SD_TAG} --chown=${UID}:${GID} /sd-server /app/sd-server
