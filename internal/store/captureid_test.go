package store

import (
	"math/big"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_CaptureIDFormat(t *testing.T) {
	seen := make(map[string]bool)
	ids := make([]string, 0, 1000)
	for range 1000 {
		id, err := NewCaptureID()
		require.NoError(t, err)
		require.Len(t, id, CaptureIDLen)
		for _, c := range id {
			require.True(t, strings.ContainsRune(base58Alphabet, c), "id %q has non-base58 %q", id, c)
		}
		require.False(t, seen[id], "duplicate id %q", id)
		seen[id] = true
		ids = append(ids, id)
	}
	assert.True(t, sort.StringsAreSorted(ids), "ids should sort by creation order")
}

func TestStore_CaptureIDEncodeBase58Fixed(t *testing.T) {
	var zero, one, max [16]byte
	one[15] = 1
	for i := range max {
		max[i] = 0xff
	}
	assert.Equal(t, strings.Repeat("1", CaptureIDLen), encodeBase58Fixed(zero))
	assert.Equal(t, strings.Repeat("1", CaptureIDLen-1)+"2", encodeBase58Fixed(one))

	// Cross-check against math/big for a few values.
	for _, b := range [][16]byte{max, {0x01, 0x9a, 0x3b, 0x00, 0x11}, {15: 57}, {15: 58}} {
		n := new(big.Int).SetBytes(b[:])
		want := make([]byte, CaptureIDLen)
		base := big.NewInt(58)
		mod := new(big.Int)
		for i := CaptureIDLen - 1; i >= 0; i-- {
			n.DivMod(n, base, mod)
			want[i] = base58Alphabet[mod.Int64()]
		}
		assert.Equal(t, string(want), encodeBase58Fixed(b))
	}

	// Fixed width keeps numeric order and string order the same.
	assert.Less(t, encodeBase58Fixed([16]byte{15: 57}), encodeBase58Fixed([16]byte{15: 58}))
}
