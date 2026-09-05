#!/bin/bash
# Container entrypoint: turn LLAMA_SWAP_* environment variables into llama-swap
# flags, so the server can be configured from a compose file or a Kubernetes
# manifest without rewriting its command line.
#
# The mapping is mechanical. A flag in llama-swap.go becomes LLAMA_SWAP_ plus
# its name, uppercased, with dashes as underscores:
#
#   -config          LLAMA_SWAP_CONFIG
#   -config-dir      LLAMA_SWAP_CONFIG_DIR
#   -listen          LLAMA_SWAP_LISTEN
#   -tls-cert-file   LLAMA_SWAP_TLS_CERT_FILE
#   -tls-key-file    LLAMA_SWAP_TLS_KEY_FILE
#   -listen-tailcat  LLAMA_SWAP_LISTEN_TAILCAT
#   -watch-config    LLAMA_SWAP_WATCH_CONFIG    (boolean)
#   -validate        LLAMA_SWAP_VALIDATE        (boolean)
#
# An unset or empty variable contributes nothing, so the flag keeps whatever
# default llama-swap itself applies.
#
# -version is deliberately not mapped. As an environment variable it would only
# make the container print a version and exit, and LLAMA_SWAP_VERSION is the
# name someone would most plausibly already be using to record which release
# they are running. Use `docker run <image> -version` for that instead.

set -euo pipefail

BIN=llama-swap

# Backwards compatibility, and the reason this is not simply additive.
#
# Before this script existed the image was ENTRYPOINT ["llama-swap"] with its
# defaults in CMD, so passing any argument to the container replaced all of
# them. Arguments therefore still win outright: anyone already running
# `docker run <image> -config /models/my.yaml` gets exactly the command line
# they got before, with no flags of ours silently added underneath.
if [[ $# -gt 0 ]]; then
    exec "$BIN" "$@"
fi

# Flags that take a value, and flags that are on/off. Keep both lists in sync
# with the flag definitions in llama-swap.go.
STRING_FLAGS=(config config-dir listen tls-cert-file tls-key-file listen-tailcat)
BOOL_FLAGS=(watch-config validate)

env_name() {
    local flag="$1"
    printf 'LLAMA_SWAP_%s' "$(printf '%s' "${flag}" | tr 'a-z-' 'A-Z_')"
}

# Only a real boolean is accepted. Quietly reading a typo as "off" would leave
# the server running with a setting the operator believes they turned on.
parse_bool() {
    local name="$1" value="$2"
    case "${value,,}" in
        1|true|yes|on)  return 0 ;;
        0|false|no|off) return 1 ;;
        *)
            echo "run.sh: ${name}='${value}' is not a boolean; use true or false" >&2
            exit 1
            ;;
    esac
}

# The defaults the image shipped in CMD, so a container started with no
# arguments and no environment behaves exactly as it did before.
: "${LLAMA_SWAP_LISTEN:=0.0.0.0:8080}"
: "${LLAMA_SWAP_WATCH_CONFIG:=true}"

# The config default applies only when neither config source is named. Adding it
# alongside a LLAMA_SWAP_CONFIG_DIR would silently merge the example models this
# image ships into the operator's own set, since -config-dir is additive.
if [[ -z "${LLAMA_SWAP_CONFIG:-}" && -z "${LLAMA_SWAP_CONFIG_DIR:-}" ]]; then
    LLAMA_SWAP_CONFIG=/etc/llama-swap/config/config.yaml
fi

args=()

for flag in "${STRING_FLAGS[@]}"; do
    var="$(env_name "${flag}")"
    value="${!var:-}"
    if [[ -n "${value}" ]]; then
        args+=("-${flag}" "${value}")
    fi
done

for flag in "${BOOL_FLAGS[@]}"; do
    var="$(env_name "${flag}")"
    value="${!var:-}"
    if [[ -n "${value}" ]] && parse_bool "${var}" "${value}"; then
        args+=("-${flag}")
    fi
done

echo "run.sh: ${BIN} ${args[*]}" >&2
exec "$BIN" "${args[@]}"
