# Rootless variant: the finished runtime image plus a non-root user owning the
# config and model directories.
#
# Its own file rather than an inline heredoc so the build can name a base image
# explicitly. That base is resolved from the local docker image store, which is
# why build-image.sh builds this one with the `default` builder: the CI builder
# is a docker-container driver, and that driver resolves FROM through the
# registry, so it silently picked up the *previously published* runtime image
# instead of the one just built.

ARG BASE_IMAGE
FROM ${BASE_IMAGE}

USER root
RUN groupadd --system --gid 10001 llama-swap && \
    useradd --system --uid 10001 --gid 10001 \
      --home /app --shell /sbin/nologin llama-swap && \
    chown -R 10001:10001 /etc/llama-swap /models

USER 10001
