#!/bin/bash
# Checks the LLAMA_SWAP_* -> flag mapping in run.sh, including the backwards
# compatible behaviour of passing arguments to the container.
#
# Runs run.sh against a stub llama-swap that prints its argument vector, so no
# image and no built binary are needed:
#
#   ./run_test.sh

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STUB_DIR="$(mktemp -d)"
trap 'rm -rf "${STUB_DIR}"' EXIT

# Prints one argument per line so an empty argument or one containing spaces is
# still visible in the comparison.
cat > "${STUB_DIR}/llama-swap" <<'STUB'
#!/bin/bash
printf '[%s]' "$@"
printf '\n'
STUB
chmod +x "${STUB_DIR}/llama-swap"

PASS=0
FAIL=0

# check <name> <expected> [VAR=value ...] -- [args...]
check() {
    local name="$1" expected="$2"; shift 2
    local envs=() cli=()
    while [[ $# -gt 0 && "$1" != "--" ]]; do envs+=("$1"); shift; done
    [[ "${1:-}" == "--" ]] && shift
    cli=("$@")

    local actual
    actual="$(env -i PATH="${STUB_DIR}:/usr/bin:/bin" "${envs[@]}" \
        bash "${SCRIPT_DIR}/run.sh" "${cli[@]}" 2>/dev/null)"

    if [[ "${actual}" == "${expected}" ]]; then
        echo "ok   ${name}"
        PASS=$((PASS + 1))
    else
        echo "FAIL ${name}"
        echo "       expected: ${expected}"
        echo "       actual:   ${actual}"
        FAIL=$((FAIL + 1))
    fi
}

DEFAULTS='[-config][/etc/llama-swap/config/config.yaml][-listen][0.0.0.0:8080][-watch-config]'

# Unchanged behaviour: no arguments and no environment reproduces the command
# line the image used to carry in CMD.
check "no args, no env" "${DEFAULTS}"

# Unchanged behaviour: arguments replace everything, exactly as overriding CMD
# did before this script existed.
check "args replace defaults" '[-config][/models/my.yaml]' -- -config /models/my.yaml
check "args win over env" '[-listen][:9999]' \
    LLAMA_SWAP_CONFIG=/from/env.yaml -- -listen :9999

check "config from env" \
    '[-config][/models/my.yaml][-listen][0.0.0.0:8080][-watch-config]' \
    LLAMA_SWAP_CONFIG=/models/my.yaml
check "listen from env" \
    '[-config][/etc/llama-swap/config/config.yaml][-listen][:9090][-watch-config]' \
    LLAMA_SWAP_LISTEN=:9090

# config-dir suppresses the default -config so the image's example models are
# not merged into the operator's own set.
check "config-dir alone" \
    '[-config-dir][/models/conf.d][-listen][0.0.0.0:8080][-watch-config]' \
    LLAMA_SWAP_CONFIG_DIR=/models/conf.d
check "config and config-dir" \
    '[-config][/a.yaml][-config-dir][/models/conf.d][-listen][0.0.0.0:8080][-watch-config]' \
    LLAMA_SWAP_CONFIG=/a.yaml LLAMA_SWAP_CONFIG_DIR=/models/conf.d

check "watch-config off" \
    '[-config][/etc/llama-swap/config/config.yaml][-listen][0.0.0.0:8080]' \
    LLAMA_SWAP_WATCH_CONFIG=false
check "watch-config off, 0" \
    '[-config][/etc/llama-swap/config/config.yaml][-listen][0.0.0.0:8080]' \
    LLAMA_SWAP_WATCH_CONFIG=0
check "watch-config on, mixed case" "${DEFAULTS}" LLAMA_SWAP_WATCH_CONFIG=True

check "validate" \
    '[-config][/etc/llama-swap/config/config.yaml][-listen][0.0.0.0:8080][-watch-config][-validate]' \
    LLAMA_SWAP_VALIDATE=yes

check "tls flags" \
    '[-config][/etc/llama-swap/config/config.yaml][-listen][0.0.0.0:8080][-tls-cert-file][/c.pem][-tls-key-file][/k.pem][-watch-config]' \
    LLAMA_SWAP_TLS_CERT_FILE=/c.pem LLAMA_SWAP_TLS_KEY_FILE=/k.pem
check "listen-tailcat" \
    '[-config][/etc/llama-swap/config/config.yaml][-listen][0.0.0.0:8080][-listen-tailcat][/k.json][-watch-config]' \
    LLAMA_SWAP_LISTEN_TAILCAT=/k.json

# An empty variable contributes nothing rather than an empty flag value.
check "empty var is ignored" "${DEFAULTS}" LLAMA_SWAP_LISTEN_TAILCAT=

# A value containing spaces survives as one argument.
check "value with spaces" \
    '[-config][/models/my config.yaml][-listen][0.0.0.0:8080][-watch-config]' \
    LLAMA_SWAP_CONFIG="/models/my config.yaml"

# -version is not mapped: a value that is not a boolean must not make the
# container refuse to start or print a version and exit.
check "LLAMA_SWAP_VERSION is inert" "${DEFAULTS}" LLAMA_SWAP_VERSION=v198

# A non-boolean for a boolean flag is refused rather than read as "off".
if bad_out="$(env -i PATH="${STUB_DIR}:/usr/bin:/bin" LLAMA_SWAP_WATCH_CONFIG=maybe \
        bash "${SCRIPT_DIR}/run.sh" 2>&1)"; then
    echo "FAIL bad boolean is rejected (exited 0)"
    FAIL=$((FAIL + 1))
elif [[ "${bad_out}" != *"is not a boolean"* ]]; then
    echo "FAIL bad boolean is rejected (no explanation: ${bad_out})"
    FAIL=$((FAIL + 1))
else
    echo "ok   bad boolean is rejected"
    PASS=$((PASS + 1))
fi

echo ""
echo "${PASS} passed, ${FAIL} failed"
[[ ${FAIL} -eq 0 ]]
