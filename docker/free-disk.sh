#!/bin/bash

# Reclaim ~30GB on the GitHub-hosted ubuntu-latest runner so large GPU
# image builds (rocm/cuda) don't ENOSPC partway through. Only invoked
# from the build workflow for backends known to need it.
#
# Destructive: rm -rf's /usr/share/dotnet, /opt/ghc, /opt/hostedtoolcache,
# /usr/local/lib/android. On a dev laptop those are real installs, not
# disposable runner state. Refuses to run unless GITHUB_ACTIONS=true.

set -euo pipefail

if [ "${GITHUB_ACTIONS:-}" != "true" ]; then
    echo "free-disk.sh refuses to run outside GitHub Actions." >&2
    echo "It deletes /usr/share/dotnet, /opt/ghc, /opt/hostedtoolcache, and" >&2
    echo "/usr/local/lib/android — only safe on a disposable runner." >&2
    exit 1
fi

echo "Before cleanup:"
df -h
sudo rm -rf /usr/share/dotnet
sudo rm -rf /usr/local/lib/android
sudo rm -rf /opt/ghc
sudo rm -rf /opt/hostedtoolcache/CodeQL
sudo docker system prune -af
echo "After cleanup:"
df -h
