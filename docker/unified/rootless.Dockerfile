# Rootless variant of the unified image: the finished runtime image plus a
# non-root user owning the config and model directories.
#
# Kept as its own Dockerfile so it can be built (and pushed) on top of an
# already published runtime image, instead of being chained onto the end of
# the assemble build -- that build ran before the runtime image existed in the
# registry, so the FROM below could not resolve it.

ARG BASE_IMAGE
FROM ${BASE_IMAGE}

USER root
RUN groupadd --system --gid 10001 llama-swap && \
    useradd --system --uid 10001 --gid 10001 \
      --home /app --shell /sbin/nologin llama-swap && \
    chown -R 10001:10001 /etc/llama-swap /models

USER 10001
