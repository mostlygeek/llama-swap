package capcompat

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
)

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "quoted template kwarg",
			in:   `llama-server --chat-template-kwargs '{"enable_thinking":false}' -c 4096`,
			want: []string{"llama-server", "--chat-template-kwargs", `{"enable_thinking":false}`, "-c", "4096"},
		},
		{
			name: "dns quotes with spaces",
			in:   `llama-server -m "a b.gguf" -c 8192`,
			want: []string{"llama-server", "-m", "a b.gguf", "-c", "8192"},
		},
		{
			name: "newlines and tabs",
			in:   "llama-server\n  -c\n	3 Apex",
			want: []string{"llama-server", "-c", "3", "Apex"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitArgs(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("splitArgs(%q) = %q (%d), want %q (%d)", tc.in, got, len(got), tc.want, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("token %d: got %q want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestOfflineContextFromCmd(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want int
	}{
		{
			name: "space form",
			cmd:  "env CUDA_DEVICE_ORDER=PCI_BUS_ID llama-server --host 0.0.0.0 --port ${PORT}\n-m /models/x.gguf\n-ngl 99\n-c 262144",
			want: 262144,
		},
		{
			name: "equals form",
			cmd:  "llama-server -m x.gguf --ctx-size=8192",
			want: 8192,
		},
		{
			name: "short equals form",
			cmd:  "llama-server -c=16384",
			want: 16384,
		},
		{
			name: "no ctx flag",
			cmd:  "llama-server -m x.gguf -ngl 99",
			want: 0,
		},
		{
			name: "flag value is a macro placeholder",
			cmd:  "llama-server -m x.gguf -c ${CTX}",
			want: 0,
		},
		{
			name: "flag value is negative",
			cmd:  "llama-server -m x.gguf -c -1",
			want: 0,
		},
		{
			name: "first parsable -c wins, even across programs in one line",
			cmd:  "other-server -c 4096 llama-server -m x.gguf --ctx-size 512",
			want: 4096,
		},
		{
			name: "first parsable flag wins",
			cmd:  "llama-server -c 4096 --ctx-size 131072",
			want: 4096,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := offlineContextFromCmd(tc.cmd); got != tc.want {
				t.Errorf("offlineContextFromCmd(%q) = %d, want %d", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestEstimateOffline(t *testing.T) {
	mc := config.ModelConfig{Cmd: "llama-server -m x.gguf -c 262144 --jinja"}
	caps, ok := EstimateOffline(mc)
	if !ok {
		t.Fatal("EstimateOffline() = not found, want found")
	}
	if caps.Context != 262144 {
		t.Errorf("Context = %d, want 262144", caps.Context)
	}

	mc = config.ModelConfig{Cmd: "llama-server -m x.gguf"}
	if _, ok := EstimateOffline(mc); ok {
		t.Error("EstimateOffline() = found, want not found")
	}
}
