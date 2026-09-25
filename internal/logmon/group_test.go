package logmon

import (
	"bytes"
	"strings"
	"testing"
)

func TestGroup_RoutesSelectedStreamsToStdout(t *testing.T) {
	var stdout bytes.Buffer
	g := NewGroup(&stdout, true, false, true)

	g.ProxyLogs.Info("PROXYLINE")
	g.UpstreamLogs.Info("UPSTREAMLINE")
	g.HttpLogs.Info("HTTPLINE")

	for name, got := range map[string]string{
		"stdout": stdout.String(),
		"mux":    string(g.MuxLogs.GetHistory()),
	} {
		if !strings.Contains(got, "PROXYLINE") || !strings.Contains(got, "HTTPLINE") {
			t.Errorf("%s missing proxy or http line: %q", name, got)
		}
		if strings.Contains(got, "UPSTREAMLINE") {
			t.Errorf("%s should not contain upstream line: %q", name, got)
		}
	}

	// Every stream keeps its own history, whether it reaches stdout or not.
	if h := string(g.UpstreamLogs.GetHistory()); !strings.Contains(h, "UPSTREAMLINE") {
		t.Errorf("upstream history = %q", h)
	}
	if h := string(g.ProxyLogs.GetHistory()); strings.Contains(h, "HTTPLINE") {
		t.Errorf("proxy history should not contain http line: %q", h)
	}
}

func TestGroup_NoStreamsDiscardsStdout(t *testing.T) {
	var stdout bytes.Buffer
	g := NewGroup(&stdout, false, false, false)

	g.ProxyLogs.Info("PROXYLINE")
	g.UpstreamLogs.Info("UPSTREAMLINE")
	g.HttpLogs.Info("HTTPLINE")

	if stdout.Len() != 0 {
		t.Errorf("stdout should be empty, got %q", stdout.String())
	}
	if h := g.MuxLogs.GetHistory(); len(h) != 0 {
		t.Errorf("mux history should be empty, got %q", h)
	}
}

func TestGroup_Stream(t *testing.T) {
	g := NewGroup(&bytes.Buffer{}, true, true, true)
	for name, want := range map[string]*Monitor{
		StreamProxy:    g.ProxyLogs,
		StreamUpstream: g.UpstreamLogs,
		StreamHTTP:     g.HttpLogs,
	} {
		if got, ok := g.Stream(name); !ok || got != want {
			t.Errorf("Stream(%q) = %p, %v; want %p", name, got, ok, want)
		}
	}
	if _, ok := g.Stream("mux"); ok {
		t.Error("Stream(\"mux\") should not resolve")
	}
}

func TestGroup_SetLogLevel(t *testing.T) {
	g := NewGroup(&bytes.Buffer{}, true, true, true)
	g.SetLogLevel(LevelWarn)

	for _, m := range []*Monitor{g.ProxyLogs, g.UpstreamLogs, g.HttpLogs} {
		m.Info("hidden")
		m.Warn("shown")
		h := string(m.GetHistory())
		if strings.Contains(h, "hidden") || !strings.Contains(h, "shown") {
			t.Errorf("history = %q", h)
		}
	}
}
