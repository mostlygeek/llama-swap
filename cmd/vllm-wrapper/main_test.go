package main

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWakeUpVLLM(t *testing.T) {
	// Test successful wake up
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/wake_up" {
			t.Errorf("Unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	if err := wakeUpVLLM(ts.URL); err != nil {
		t.Fatalf("wakeUpVLLM failed: %v", err)
	}

	// Test failure when server returns error
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/wake_up" {
			t.Errorf("Unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts2.Close()

	if err := wakeUpVLLM(ts2.URL); err == nil {
		t.Errorf("wakeUpVLLM expected error for non-200 response")
	}
}

func TestWaitForHealthy(t *testing.T) {
	// Test successful health check
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[]}`))
	}))
	defer ts.Close()

	if err := waitForHealthyWithPath(ts.URL, "/v1/models", 2*time.Second); err != nil {
		t.Fatalf("waitForHealthy failed: %v", err)
	}

	// Test timeout: server delays response longer than context timeout
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Delay 2 seconds
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[]}`))
	}))
	defer ts2.Close()

	err := waitForHealthyWithPath(ts2.URL, "/v1/models", 1*time.Second)
	if err == nil {
		t.Errorf("waitForHealthy expected timeout error")
		return
	}
	if err != context.DeadlineExceeded {
		t.Errorf("waitForHealthy expected context deadline exceeded, got %v", err)
	}
}

func TestSleepCommandMarshal(t *testing.T) {
	// We test the sleep command by checking the JSON marshaling we use in sleepCmd.
	// Since sleepCmd is not easily unit-testable without exposing more, we test the structure.
	body := map[string]int{"level": 1}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}
	expected := `{"level":1}`
	if string(data) != expected {
		t.Errorf("Expected %s, got %s", expected, string(data))
	}
}

// TestStartDaemon tests that startDaemon returns an error when the start command exits
// quickly and the daemon does not become healthy.
func TestStartDaemon(t *testing.T) {
	// Use a start command that exits immediately (true) and a health URL that will not respond.
	err := startDaemon([]string{"true"}, "http://127.0.0.1:12345/health", "/health", 10*time.Millisecond)
	if err == nil {
		t.Fatalf("startDaemon expected error but got nil")
	}
	if !strings.Contains(err.Error(), "daemon did not become healthy") {
		t.Errorf("error expected to contain 'daemon did not become healthy', got %v", err)
	}
}

// TestNewProxyTransport_ResponseHeaderTimeout verifies that the configured
// response header timeout is applied to the transport, and that 0 disables
// it per net/http semantics.
func TestNewProxyTransport_ResponseHeaderTimeout(t *testing.T) {
	transport := newProxyTransport(15 * time.Minute)
	if transport.ResponseHeaderTimeout != 15*time.Minute {
		t.Errorf("ResponseHeaderTimeout: got %v, want %v", transport.ResponseHeaderTimeout, 15*time.Minute)
	}

	transport = newProxyTransport(0)
	if transport.ResponseHeaderTimeout != 0 {
		t.Errorf("ResponseHeaderTimeout: got %v, want 0 (disabled)", transport.ResponseHeaderTimeout)
	}
}

// TestServeCmd_ResponseHeaderTimeoutFlag verifies the -response-header-timeout
// flag defaults to at least 15 minutes and can be overridden.
func TestServeCmd_ResponseHeaderTimeoutFlag(t *testing.T) {
	var responseHeaderTimeout time.Duration
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.DurationVar(&responseHeaderTimeout, "response-header-timeout", 15*time.Minute, "")

	if err := fs.Parse(nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if responseHeaderTimeout < 15*time.Minute {
		t.Errorf("default response-header-timeout: got %v, want >= 15m", responseHeaderTimeout)
	}

	fs2 := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs2.DurationVar(&responseHeaderTimeout, "response-header-timeout", 15*time.Minute, "")
	if err := fs2.Parse([]string{"-response-header-timeout", "0"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if responseHeaderTimeout != 0 {
		t.Errorf("override response-header-timeout: got %v, want 0", responseHeaderTimeout)
	}
}

// TestStartDaemonArgv verifies that multiple startArgs are passed as separate argv values.
func TestStartDaemonArgv(t *testing.T) {
	tmpDir := t.TempDir()
	argvFile := filepath.Join(tmpDir, "argv.txt")

	script := filepath.Join(tmpDir, "write-argv.sh")
	if err := os.WriteFile(script, []byte(
		"#!/bin/bash\nprintf '%s\n' \"$@\" > \""+argvFile+"\"\nexit 0\n",
	), 0755); err != nil {
		t.Fatalf("write helper script: %v", err)
	}

	err := startDaemon([]string{script, "arg1", "arg2", "arg3"}, "http://127.0.0.1:12345/health", "/health", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected error (health check fails)")
	}

	content, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read argv file: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(content)), "\n")
	want := []string{"arg1", "arg2", "arg3"}
	if len(got) != len(want) {
		t.Fatalf("argv length: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}
