## Container Security

For convenience, the default container images use the **root** user within the container. This permits simplified access to host resources including volume mounts and hardware devices under `/dev/dri` (_for Vulkan support_). But this can widen the attack surface to privilege escalation exploits.

Alternative images, tagged as `non-root`, are also available. For example, `llama-swap:cpu-non-root` uses the unprivileged **app** user by default. Depending on deployment requirements, additional configuration may be necessary to ensure that the container retains access to required hosts resources. This might entail customizing host filesystem permissions/ownership appropriately or injecting host group membership into the container.

Docker offers a [system-wide option enabling user namespace remapping](https://docs.docker.com/engine/security/userns-remap/) to accommodate situations were a **root** container user is required but also mentions that _"The best way to prevent privilege-escalation attacks from within a container is to configure your container's applications to run as unprivileged users."_ Podman offers similar capability, per-container, to [set UID/GID mapping in a new user namespace](https://docs.podman.io/en/latest/markdown/podman-run.1.html#set-uid-gid-mapping-in-a-new-user-namespace).

The Large Language Model (_LLM/AI_) ecosystem is rapidly evolving and [serious security vulnerabilities have surfaced in the past](https://huggingface.co/docs/hub/security-pickle). These alternative _non-root_ images could reduce the impact of future unknown problems. However, proper planning and configuration is recommended to utilize them.
