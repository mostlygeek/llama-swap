package store

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStore_CaptureIDFormat(t *testing.T) {
	at := time.Date(2026, 10, 31, 20, 59, 59, 0, time.UTC)
	for range 1000 {
		id := NewCaptureID(at)
		assert.Len(t, id, CaptureIDLen)
		assert.True(t, strings.HasPrefix(id, "26103120-"), "id %q", id)
		assert.True(t, ValidCaptureID(id), "id %q", id)
	}

	// The prefix is the UTC hour, whatever the time zone of t.
	local := time.Date(2026, 1, 2, 8, 0, 0, 0, time.FixedZone("UTC+5", 5*3600))
	assert.True(t, strings.HasPrefix(NewCaptureID(local), "26010203-"))
}

func TestStore_CaptureIDSuffixVaries(t *testing.T) {
	at := time.Now()
	seen := make(map[string]bool)
	for range 200 {
		seen[NewCaptureID(at)] = true
	}
	// 200 draws from ~11.3 million suffixes: a repeat is possible but more
	// than a couple means the suffix is not random.
	assert.Greater(t, len(seen), 197)
}

func TestStore_CaptureIDValid(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{"26103120-a7Kq", true},
		{"26103120-a7K", false},   // short
		{"26103120_a7Kq", false},  // wrong separator
		{"26133120-a7Kq", false},  // month 13
		{"26103120-a0Kq", false},  // 0 is not base58
		{"26103120-a7Kq1", false}, // long
		{"", false},
	} {
		assert.Equal(t, tc.want, ValidCaptureID(tc.id), tc.id)
	}
}
