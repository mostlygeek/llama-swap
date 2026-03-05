package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProxyManager_HasImageContent(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			"text only - string content",
			`{"model":"test","messages":[{"role":"user","content":"hello"}]}`,
			false,
		},
		{
			"text only - array content",
			`{"model":"test","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`,
			false,
		},
		{
			"with image_url",
			`{"model":"test","messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,abc"}}]}]}`,
			true,
		},
		{
			"empty messages",
			`{"model":"test","messages":[]}`,
			false,
		},
		{
			"no messages key",
			`{"model":"test"}`,
			false,
		},
		{
			"image in earlier message",
			`{"model":"test","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]},{"role":"assistant","content":"ok"},{"role":"user","content":"more?"}]}`,
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasImageContent([]byte(tt.body))
			assert.Equal(t, tt.want, got)
		})
	}
}
