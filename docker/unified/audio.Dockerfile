# audio.cpp -> /install
ARG BUILDER_BASE
# The previous build's compiler cache, or the builder base when none has been
# published yet. See ccache-build.sh.
ARG CCACHE_SEED=${BUILDER_BASE}
FROM ${CCACHE_SEED} AS ccache-seed

FROM ${BUILDER_BASE} AS compile

ARG BACKEND=cuda
ARG AUDIO_COMMIT_HASH=main
ARG COMPILE_TIMEOUT_MINUTES=
ARG CCACHE_EXPORT=
COPY ccache-build.sh install-audio.sh /build/
RUN for i in 1 2 3 4 5; do apt-get -o Acquire::Retries=5 update --error-on=any && break; \
      [ "$i" -lt 5 ] || exit 1; echo "apt-get update failed, retrying in $((i * 30))s"; sleep $((i * 30)); \
    done \
    && apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
    libmp3lame-dev \
    && rm -rf /var/lib/apt/lists/*
RUN --mount=type=bind,from=ccache-seed,target=/ccache-seed \
    --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=audio-${BACKEND},target=/src/audio.cpp/build \
    COMPILE_TIMEOUT_MINUTES=${COMPILE_TIMEOUT_MINUTES} CCACHE_EXPORT=${CCACHE_EXPORT} \
    BACKEND=${BACKEND} bash /build/ccache-build.sh run bash /build/install-audio.sh "${AUDIO_COMMIT_HASH}"

# The compiler cache, published by build-image.sh --stage for the next run.
FROM scratch AS ccache
COPY --from=compile /ccache-out /ccache

# Fails here, after the cache stage, when the install script did not finish.
FROM compile AS build
RUN bash /build/ccache-build.sh check

FROM scratch
COPY --from=build /install /install
