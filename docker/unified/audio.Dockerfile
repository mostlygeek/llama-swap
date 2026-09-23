# audio.cpp -> /install
ARG BUILDER_BASE
FROM ${BUILDER_BASE} AS build

ARG BACKEND=cuda
ARG AUDIO_COMMIT_HASH=main
COPY install-audio.sh /build/
RUN apt-get update && apt-get install -y --no-install-recommends \
    libmp3lame-dev \
    && rm -rf /var/lib/apt/lists/*
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=audio-${BACKEND},target=/src/audio.cpp/build \
    BACKEND=${BACKEND} bash /build/install-audio.sh "${AUDIO_COMMIT_HASH}"

FROM scratch
COPY --from=build /install /install
