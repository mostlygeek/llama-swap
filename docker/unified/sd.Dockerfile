# stable-diffusion.cpp -> /install
ARG BUILDER_BASE
FROM ${BUILDER_BASE} AS build

ARG BACKEND=cuda
ARG SD_COMMIT_HASH=master

# Download and install nvm:
# https://github.com/nvm-sh/nvm#installing-in-docker

# Use bash for the shell
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# Create a script file sourced by both interactive and non-interactive bash shells
ENV BASH_ENV "${HOME}/.bash_env"
RUN touch "${BASH_ENV}"
RUN echo '. "${BASH_ENV}"' >> ~/.bashrc

# Download and install nvm
RUN curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.5/install.sh | PROFILE="${BASH_ENV}" bash
RUN echo node > .nvmrc
RUN nvm install 22

RUN npm install -g pnpm@latest-10

COPY install-sd.sh /build/
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=sd-${BACKEND},target=/src/stable-diffusion.cpp/build \
    BACKEND=${BACKEND} bash /build/install-sd.sh "${SD_COMMIT_HASH}"

FROM scratch
COPY --from=build /install /install
