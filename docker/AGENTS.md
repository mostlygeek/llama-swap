- you are working in my VM sandbox. It is safe to use sudo.
- use or install whatever tools you need to complete your goal
- use DOCKER_BUILDKIT=1 docker build -t llama-swap:optimized
 - DOCKER_BUILDKIT=1 is important to use the caching
- ALWAYS send notifications to get the user's attention
- when running `./build-image.sh`, use a 2-hour (7200000ms) timeout minimum as CUDA builds take 60-120+ minutes to compile for multiple architectures

# Notifications

ALWAYS send notifications to keep the user informed:

- When starting or finishing a job
- For progress updates on long-running tasks (especially Docker builds)
- For todo list progress updates (when items start/complete)
- When you need feedback or to elicit information from the user
- use pushover.sh <message>, example: `pushover.sh "notification to send"`

