# whisper.cpp -> /install. Fastest of the compiles.
ARG BUILDER_BASE
FROM ${BUILDER_BASE} AS build

ARG BACKEND=cuda
ARG WHISPER_FFMPEG=yes
ARG WHISPER_COMMIT_HASH=master
COPY install-whisper.sh /build/
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=whisper-${BACKEND},target=/src/whisper.cpp/build \
    BACKEND=${BACKEND} WHISPER_FFMPEG=${WHISPER_FFMPEG} \
    bash /build/install-whisper.sh "${WHISPER_COMMIT_HASH}"

FROM scratch
COPY --from=build /install /install
