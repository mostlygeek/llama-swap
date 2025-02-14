ARG BASE_TAG=server-cuda
FROM ghcr.io/ggerganov/llama.cpp:${BASE_TAG}

# has to be after the FROM
ARG LS_VER=89

WORKDIR /app

RUN \
    curl -LO https://github.com/mostlygeek/llama-swap/releases/download/v"${LS_VER}"/llama-swap_"${LS_VER}"_linux_amd64.tar.gz && \
    tar -zxf llama-swap_"${LS_VER}"_linux_amd64.tar.gz && \
    rm llama-swap_"${LS_VER}"_linux_amd64.tar.gz


ENTRYPOINT [ "/app/llama-swap", "--config", "/config.yaml" ]