#!/bin/bash
set -e

# Define a function to export immediately AND save to .bashrc for future sessions
export_persist() {
    local var_name="$1"
    local var_value="$2"
    
    # 1. Export for the current running process
    export "$var_name"="$var_value"
    
    # 2. Append to .bashrc (idempotent check to avoid duplicate lines)
    if ! grep -q "export $var_name=" ~/.bashrc; then
        echo "export $var_name=\"$var_value\"" >> ~/.bashrc
    else
        # Optional: Update the existing line if it exists
        sed -i "s|export $var_name=.*|export $var_name=\"$var_value\"|" ~/.bashrc
    fi
}

# --- Help Function ---
usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Required Arguments:"
    echo "  -r, --role <head|node>      : Set the node type"
    echo "  -h, --host-ip <ip>          : IP address of this interface (Host IP)"
    echo "  -e, --eth-if <name>         : Ethernet interface name (e.g., eth0)"
    echo "  -i, --ib-if <name>          : InfiniBand/RDMA interface name"
    echo ""
    echo "Conditional Arguments:"
    echo "  -m, --head-ip <ip>          : IP of the head node (REQUIRED if role is 'node')"
    echo ""
    echo "Example:"
    echo "  $0 --role head --host-ip 192.168.1.10 --eth-if eth0 --ib-if ib0"
    echo "  $0 --role node --host-ip 192.168.1.20 --eth-if eth0 --ib-if ib0 --head-ip 192.168.1.10"
    exit 1
}

# --- Argument Parsing ---

# Initialize variables to empty
NODE_TYPE=""
HOST_IP=""
ETH_IF_NAME=""
IB_IF_NAME=""
HEAD_IP=""

while [[ "$#" -gt 0 ]]; do
    case $1 in
        -r|--role) NODE_TYPE="$2"; shift ;;
        -h|--host-ip) HOST_IP="$2"; shift ;;
        -e|--eth-if) ETH_IF_NAME="$2"; shift ;;
        -i|--ib-if) IB_IF_NAME="$2"; shift ;;
        -m|--head-ip) HEAD_IP="$2"; shift ;;
        *) echo "Unknown parameter passed: $1"; usage ;;
    esac
    shift
done

# --- Validation ---

# 1. Check if all common required arguments are present
if [[ -z "$NODE_TYPE" || -z "$HOST_IP" || -z "$ETH_IF_NAME" || -z "$IB_IF_NAME" ]]; then
    echo "Error: Missing required arguments."
    usage
fi

# 2. Validate Role
if [[ "$NODE_TYPE" != "head" && "$NODE_TYPE" != "node" ]]; then
    echo "Error: --role must be 'head' or 'node'."
    exit 1
fi

# 3. Conditional Check for Head IP
if [[ "$NODE_TYPE" == "node" && -z "$HEAD_IP" ]]; then
    echo "Error: When --role is 'node', you must provide --head-ip."
    exit 1
fi

# --- Environment Configuration ---

echo "Configuring environment for [$NODE_TYPE] at $HOST_IP..."

export_persist VLLM_HOST_IP "$HOST_IP"
export_persist RAY_NODE_IP_ADDRESS "$HOST_IP"
export_persist RAY_OVERRIDE_NODE_IP_ADDRESS "$HOST_IP"

# Network Interface
export_persist MN_IF_NAME "$ETH_IF_NAME"
export_persist UCX_NET_DEVICES "$ETH_IF_NAME"
export_persist NCCL_SOCKET_IFNAME "$ETH_IF_NAME"

# InfiniBand
export_persist NCCL_IB_HCA "$IB_IF_NAME"
export_persist NCCL_IB_DISABLE "0"

# Sockets/Transport
export_persist OMPI_MCA_btl_tcp_if_include "$ETH_IF_NAME"
export_persist GLOO_SOCKET_IFNAME "$ETH_IF_NAME"
export_persist TP_SOCKET_IFNAME "$ETH_IF_NAME"
export_persist RAY_memory_monitor_refresh_ms "0"

# --- Execution ---

if [ "${NODE_TYPE}" == "head" ]; then
    echo "Starting Ray HEAD node..."
    exec ray start --block --head --port 6379 \
        --node-ip-address "$VLLM_HOST_IP" \
	--include-dashboard=True \
        --dashboard-host "0.0.0.0" \
        --dashboard-port 8265 \
        --disable-usage-stats
else
    echo "Starting Ray WORKER node connecting to $HEAD_IP..."
    exec ray start --block \
        --address="$HEAD_IP:6379" \
        --node-ip-address "$VLLM_HOST_IP"
fi

