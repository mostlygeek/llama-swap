package store

import (
	"crypto/rand"
	"strings"
	"time"
)

// CaptureIDLen is the length of every capture ID.
const CaptureIDLen = len(captureIDTimeLayout) + 1 + captureIDSuffixLen

// captureIDTimeLayout is the yymmddhh prefix of a capture ID, in UTC.
const captureIDTimeLayout = "06010215"

// captureIDSuffixLen is the number of random base58 characters after the
// prefix. 58^4 is about 11.3 million IDs per hour.
const captureIDSuffixLen = 4

// base58Alphabet leaves out 0, O, I and l so IDs are easy to read and type.
const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// NewCaptureID returns a capture ID for a request recorded at t, such as
// "26103120-a7Kq": the UTC hour as yymmddhh, a dash and four random base58
// characters. IDs are unique enough rather than guaranteed unique; a
// CaptureRepository rejects a duplicate instead of overwriting the earlier
// capture.
func NewCaptureID(t time.Time) string {
	var b strings.Builder
	b.Grow(CaptureIDLen)
	b.WriteString(t.UTC().Format(captureIDTimeLayout))
	b.WriteByte('-')

	// Rejection sampling keeps every character equally likely: bytes at or
	// above the largest multiple of 58 that fits in a byte are discarded.
	const limit = 256 - 256%len(base58Alphabet)
	var buf [16]byte
	for n := 0; n < captureIDSuffixLen; {
		rand.Read(buf[:])
		for _, c := range buf {
			if int(c) >= limit {
				continue
			}
			b.WriteByte(base58Alphabet[int(c)%len(base58Alphabet)])
			if n++; n == captureIDSuffixLen {
				break
			}
		}
	}
	return b.String()
}

// ValidCaptureID reports whether id has the shape NewCaptureID produces.
func ValidCaptureID(id string) bool {
	if len(id) != CaptureIDLen || id[len(captureIDTimeLayout)] != '-' {
		return false
	}
	if _, err := time.Parse(captureIDTimeLayout, id[:len(captureIDTimeLayout)]); err != nil {
		return false
	}
	for _, c := range id[len(captureIDTimeLayout)+1:] {
		if !strings.ContainsRune(base58Alphabet, c) {
			return false
		}
	}
	return true
}
