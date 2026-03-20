- you are working in my VM sandbox. It is safe to use sudo.
- use or install whatever tools you need to complete your goal
- use `docker buildx build --build-arg BACKEND=cuda` or `--build-arg BACKEND=vulkan` with the unified `docker/Dockerfile`
- DOCKER_BUILDKIT=1 is required for cache mounts and conditional FROM stages
- ALWAYS send notifications to get the user's attention
- when running `./build-image.sh`, use a 2-hour (7200000ms) timeout minimum as CUDA builds take 60-120+ minutes to compile for multiple architectures

# Adding a new server project

1. Add source clone stage in `docker/Dockerfile` (FROM builder-base AS <project>-source)
2. Add build stage with CUDA/Vulkan conditional cmake flags (FROM builder-base AS <project>-build)
3. Add COPY lines in the runtime stage for binaries and libraries
4. Add the binary name(s) to the validation RUN step in the runtime stage
5. Add the repo URL and commit hash to `docker/build-image.sh`

# Notifications

ALWAYS send notifications to keep the user informed:

- When starting or finishing a job
- For progress updates on long-running tasks (especially Docker builds)
- For todo list progress updates (when items start/complete)
- When you need feedback or to elicit information from the user
- use pushover.sh <message>, example: `pushover.sh "notification to send"`
