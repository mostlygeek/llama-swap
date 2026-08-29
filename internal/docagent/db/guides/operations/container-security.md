---
title: Container security
summary: Choose root or non-root llama-swap container images and retain only the host access your deployment needs.
category: guides
tags: [containers, docker, podman, security, non-root]
config_keys: []
updated: 2026-08-29
---

# Container security

The default container images run as `root` inside the container. This makes
volume mounts and hardware devices such as `/dev/dri` convenient to use, but it
also increases the impact of a container escape or privilege-escalation flaw.

Use an image tagged `non-root` when the deployment does not require root. For
example, `llama-swap:cpu-non-root` runs as the unprivileged `app` user. Check
that mounted files and required device nodes are readable by that user; you may
need to adjust host ownership or add the needed host group to the container.

When a root container is unavoidable, Docker's [user namespace
remapping](https://docs.docker.com/engine/security/userns-remap/) can map the
container user to an unprivileged host user. Podman supports equivalent
per-container UID/GID mappings through [user
namespaces](https://docs.podman.io/en/latest/markdown/podman-run.1.html#set-uid-gid-mapping-in-a-new-user-namespace).

Treat model files and surrounding tooling as part of the security boundary.
The LLM ecosystem has had [unsafe model serialization
issues](https://huggingface.co/docs/hub/security-pickle); non-root images reduce
the impact of an unknown vulnerability but do not replace careful image,
mount, and permission review.
