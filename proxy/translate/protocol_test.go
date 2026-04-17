package translate

import "testing"

func TestDetectInbound(t *testing.T) {
	cases := []struct {
		method, path string
		wantProto    Protocol
		wantKind     EndpointKind
		wantOK       bool
	}{
		{"POST", "/v1/chat/completions", ProtocolOpenAI, KindChat, true},
		{"POST", "/v1/messages", ProtocolAnthropic, KindChat, true},
		{"POST", "/api/chat", ProtocolOllama, KindChat, true},
		{"POST", "/api/generate", ProtocolOllama, KindGenerate, true},
		{"POST", "/v1/responses", "", "", false},
		{"POST", "/v1/completions", "", "", false},
		{"POST", "/v1/embeddings", "", "", false},
		{"GET", "/v1/chat/completions", "", "", false},
	}
	for _, tc := range cases {
		p, k, ok := DetectInbound(tc.method, tc.path)
		if p != tc.wantProto || k != tc.wantKind || ok != tc.wantOK {
			t.Errorf("DetectInbound(%q,%q)=(%q,%q,%v) want (%q,%q,%v)",
				tc.method, tc.path, p, k, ok, tc.wantProto, tc.wantKind, tc.wantOK)
		}
	}
}

func TestCanonicalPath(t *testing.T) {
	cases := []struct {
		target Protocol
		kind   EndpointKind
		want   string
	}{
		{ProtocolOpenAI, KindChat, "/v1/chat/completions"},
		{ProtocolOpenAI, KindGenerate, "/v1/chat/completions"},
		{ProtocolAnthropic, KindChat, "/v1/messages"},
		{ProtocolOllama, KindChat, "/api/chat"},
		{ProtocolOllama, KindGenerate, "/api/generate"},
	}
	for _, tc := range cases {
		got := CanonicalPath(tc.target, tc.kind)
		if got != tc.want {
			t.Errorf("CanonicalPath(%q,%q)=%q want %q", tc.target, tc.kind, got, tc.want)
		}
	}
}

func TestParseProtocol(t *testing.T) {
	if _, err := ParseProtocol("openai"); err != nil {
		t.Errorf("openai: %v", err)
	}
	if _, err := ParseProtocol(" Anthropic "); err != nil {
		t.Errorf("anthropic: %v", err)
	}
	if _, err := ParseProtocol("gemini"); err == nil {
		t.Errorf("gemini should fail")
	}
}
