package swaputil

import "testing"

func TestFormatListenURL(t *testing.T) {
	tests := []struct {
		name       string
		scheme     string
		listenAddr string
		want       string
	}{
		{"wildcard host", "http", ":8080", ":8080"},
		{"wildcard host with TLS", "https", ":8443", ":8443"},
		{"ipv4 wildcard", "http", "0.0.0.0:9999", "http://0.0.0.0:9999"},
		{"explicit host", "http", "127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"localhost with TLS", "https", "localhost:8443", "https://localhost:8443"},
		{"ipv6 wildcard", "http", "[::]:8080", "http://[::]:8080"},
		{"invalid address", "http", "not-an-address", "not-an-address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatListenURL(tt.scheme, tt.listenAddr); got != tt.want {
				t.Fatalf("FormatListenURL(%q, %q) = %q, want %q", tt.scheme, tt.listenAddr, got, tt.want)
			}
		})
	}
}
