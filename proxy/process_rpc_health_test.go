package proxy

import (
	"context"
	"io"
	"testing"

	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/stretchr/testify/assert"
)

func TestProcess_RPCHealthIndependentOfState(t *testing.T) {
	testLogger := NewLogMonitorWriter(io.Discard)
	proxyLogger := NewLogMonitorWriter(io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	modelConfig := config.ModelConfig{
		Cmd:            "llama-server --rpc 127.0.0.1:50051",
		Proxy:          "http://localhost:8080",
		RPCHealthCheck: true,
	}

	process := NewProcess("test-model", 5, modelConfig, testLogger, proxyLogger, ctx)

	// Verify endpoints were parsed
	assert.NotEmpty(t, process.rpcEndpoints, "RPC endpoints should be parsed from cmd")
	assert.Equal(t, []string{"127.0.0.1:50051"}, process.rpcEndpoints)

	// Initially should be unhealthy (false) until first check
	assert.False(t, process.rpcHealthy.Load(), "RPC health should start as false")

	// Health checker should be running regardless of process state
	assert.NotNil(t, process.rpcHealthTicker, "Health checker ticker should be running")
	assert.NotNil(t, process.rpcHealthCancel, "Health checker should have cancel func")

	// Process state should not affect health checking
	assert.Equal(t, StateStopped, process.CurrentState(), "Process should be in stopped state")

	// Health check runs independently - simulate RPC becoming healthy
	process.rpcHealthy.Store(true)
	assert.True(t, process.IsRPCHealthy(), "Process should report healthy regardless of state")
}

func TestProcess_RPCHealthCheckDisabled(t *testing.T) {
	testLogger := NewLogMonitorWriter(io.Discard)
	proxyLogger := NewLogMonitorWriter(io.Discard)
	ctx := context.Background()

	modelConfig := config.ModelConfig{
		Cmd:            "llama-server --rpc 127.0.0.1:50051",
		Proxy:          "http://localhost:8080",
		RPCHealthCheck: false, // Disabled
	}

	process := NewProcess("test-model", 5, modelConfig, testLogger, proxyLogger, ctx)

	// Should always return healthy when disabled
	assert.True(t, process.IsRPCHealthy(), "Should return true when RPC health check is disabled")
}

func TestProcess_RPCHealthCheckNoEndpoints(t *testing.T) {
	testLogger := NewLogMonitorWriter(io.Discard)
	proxyLogger := NewLogMonitorWriter(io.Discard)
	ctx := context.Background()

	modelConfig := config.ModelConfig{
		Cmd:            "llama-server --port 8080", // No --rpc flag
		Proxy:          "http://localhost:8080",
		RPCHealthCheck: true, // Enabled but no endpoints
	}

	process := NewProcess("test-model", 5, modelConfig, testLogger, proxyLogger, ctx)

	// Should have no endpoints
	assert.Empty(t, process.rpcEndpoints, "Should have no RPC endpoints when --rpc flag is missing")

	// Should return healthy when no endpoints configured (treat as not using RPC)
	assert.True(t, process.IsRPCHealthy(), "Should return true when no RPC endpoints found")

	// Health checker should NOT start when no endpoints
	assert.Nil(t, process.rpcHealthTicker, "Health checker should not run without endpoints")
	assert.Nil(t, process.rpcHealthCancel, "Health checker cancel should be nil")
}

func TestProcess_RPCHealthCheckTimeoutIgnored(t *testing.T) {
	testLogger := NewLogMonitorWriter(io.Discard)
	proxyLogger := NewLogMonitorWriter(io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use an IP address that will timeout (non-routable IP)
	// 192.0.2.0/24 is reserved for documentation/testing (RFC 5737)
	modelConfig := config.ModelConfig{
		Cmd:            "llama-server --rpc 192.0.2.1:50051",
		Proxy:          "http://localhost:8080",
		RPCHealthCheck: true,
	}

	process := NewProcess("test-model", 5, modelConfig, testLogger, proxyLogger, ctx)

	// Verify endpoints were parsed
	assert.NotEmpty(t, process.rpcEndpoints, "RPC endpoints should be parsed from cmd")
	assert.Equal(t, []string{"192.0.2.1:50051"}, process.rpcEndpoints)

	// Initially should be unhealthy (false) until first check
	assert.False(t, process.rpcHealthy.Load(), "RPC health should start as false")

	// Manually run health check - this should timeout but not mark as unhealthy
	process.checkRPCHealth()

	// After timeout, should remain at initial state (false) but not be marked unhealthy
	// The key is that timeout doesn't change the state - it's effectively a no-op
	// To test this properly, let's set it to healthy first, then see if timeout changes it
	process.rpcHealthy.Store(true)
	initialState := process.rpcHealthy.Load()
	assert.True(t, initialState, "Should be healthy before timeout check")

	// Run health check that will timeout
	process.checkRPCHealth()

	// After timeout, should still be healthy (timeout is ignored)
	assert.True(t, process.rpcHealthy.Load(), "Should remain healthy after timeout")
}
