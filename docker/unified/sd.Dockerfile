# stable-diffusion.cpp -> /install
ARG BUILDER_BASE
FROM ${BUILDER_BASE} AS build

ARG BACKEND=cuda
ARG SD_COMMIT_HASH=master
COPY install-sd.sh /build/
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=sd-${BACKEND},target=/src/stable-diffusion.cpp/build \
    BACKEND=${BACKEND} bash /build/install-sd.sh "${SD_COMMIT_HASH}"

FROM scratch
COPY --from=build /install /install
