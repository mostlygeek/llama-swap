# ik_llama.cpp -> /install. CUDA only; there is no Vulkan build, and the
# workflow schedules no ik-llama job for that backend. The runtime supplies an
# empty /install/bin in its place.
ARG BUILDER_BASE
FROM ${BUILDER_BASE} AS build

ARG IK_LLAMA_COMMIT_HASH=main
COPY install-ik-llama.sh /build/
RUN --mount=type=cache,id=ccache-cuda,target=/ccache \
    --mount=type=cache,id=ik-llama-cuda,target=/src/ik_llama.cpp/build \
    bash /build/install-ik-llama.sh "${IK_LLAMA_COMMIT_HASH}"

FROM scratch
COPY --from=build /install /install
