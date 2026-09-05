# ik_llama.cpp -> /install
ARG BUILDER_BASE
FROM ${BUILDER_BASE} AS build

ARG BACKEND=cuda
ARG IK_LLAMA_COMMIT_HASH=main
COPY install-ik-llama.sh /build/
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=ik-llama-${BACKEND},target=/src/ik_llama.cpp/build \
    BACKEND=${BACKEND} bash /build/install-ik-llama.sh "${IK_LLAMA_COMMIT_HASH}"

FROM scratch
COPY --from=build /install /install
