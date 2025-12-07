ARG BASE_IMAGE=ghcr.io/ggml-org/llama.cpp
ARG BASE_TAG=server-cuda
FROM ${BASE_IMAGE}:${BASE_TAG}

# has to be after the FROM
ARG LS_VER=170
ARG LS_REPO=mostlygeek/llama-swap

# Set default UID/GID arguments
ARG UID=10001
ARG GID=10001
ARG USER_HOME=/app

# Add user/group
ENV HOME=$USER_HOME
RUN if [ $UID -ne 0 ]; then \
      if [ $GID -ne 0 ]; then \
        groupadd --system --gid $GID app; \
      fi; \
      useradd --system --uid $UID --gid $GID \
      --home $USER_HOME app; \
    fi

# Handle paths
RUN mkdir --parents $HOME /app
RUN chown --recursive $UID:$GID $HOME /app

# Switch user
USER $UID:$GID

WORKDIR /app

# Add /app to PATH
ENV PATH="/app:${PATH}"

RUN \
    curl -LO "https://github.com/${LS_REPO}/releases/download/v${LS_VER}/llama-swap_${LS_VER}_linux_amd64.tar.gz" && \
    tar -zxf "llama-swap_${LS_VER}_linux_amd64.tar.gz" && \
    rm "llama-swap_${LS_VER}_linux_amd64.tar.gz"

COPY --chown=$UID:$GID config.example.yaml /app/config.yaml

HEALTHCHECK CMD curl -f http://localhost:8080/ || exit 1
ENTRYPOINT [ "/app/llama-swap", "-config", "/app/config.yaml" ]
