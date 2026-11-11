# stable-diffusion.cpp -> /install
ARG BUILDER_BASE
# The previous build's compiler cache, or the builder base when none has been
# published yet. See ccache-build.sh.
ARG CCACHE_SEED=${BUILDER_BASE}
FROM ${CCACHE_SEED} AS ccache-seed

FROM ${BUILDER_BASE} AS compile

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
RUN curl -fsSL --retry 5 --retry-all-errors --retry-delay 10 -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.5/install.sh | PROFILE="${BASH_ENV}" bash
RUN echo node > .nvmrc
RUN nvm install 22

RUN npm install -g pnpm@latest-10

ARG COMPILE_TIMEOUT_MINUTES=
ARG CCACHE_EXPORT=
COPY ccache-build.sh install-sd.sh /build/
RUN --mount=type=bind,from=ccache-seed,target=/ccache-seed \
    --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=sd-${BACKEND},target=/src/stable-diffusion.cpp/build \
    COMPILE_TIMEOUT_MINUTES=${COMPILE_TIMEOUT_MINUTES} CCACHE_EXPORT=${CCACHE_EXPORT} \
    BACKEND=${BACKEND} bash /build/ccache-build.sh run bash /build/install-sd.sh "${SD_COMMIT_HASH}"

# The compiler cache, published by build-image.sh --stage for the next run.
FROM scratch AS ccache
COPY --from=compile /ccache-out /ccache

# Fails here, after the cache stage, when the install script did not finish.
FROM compile AS build
RUN bash /build/ccache-build.sh check

FROM scratch
COPY --from=build /install /install
