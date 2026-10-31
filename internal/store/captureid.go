package store

import (
	"fmt"

	"github.com/google/uuid"
)

// CaptureIDLen is the length of every capture ID.
const CaptureIDLen = 22

// base58Alphabet is the Bitcoin base58 alphabet. Its characters are in
// ascending ASCII order, so fixed-width encodings sort like the numbers
// they encode.
const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// NewCaptureID returns a new capture ID: a UUIDv7 encoded as fixed-width
// base58. UUIDv7 starts with a millisecond timestamp and the encoding is
// left-padded to CaptureIDLen, so IDs sort by creation time as strings.
func NewCaptureID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("new capture id: %w", err)
	}
	return encodeBase58Fixed(id), nil
}

// encodeBase58Fixed encodes the 128-bit big-endian number in b as base58,
// left-padded with the zero digit to CaptureIDLen characters. 58^22 is
// greater than 2^128, so every value fits.
func encodeBase58Fixed(b [16]byte) string {
	out := make([]byte, CaptureIDLen)
	num := b
	for i := CaptureIDLen - 1; i >= 0; i-- {
		// Divide num by 58 in place, keeping the remainder as this digit.
		var rem uint
		for j := range num {
			acc := rem<<8 | uint(num[j])
			num[j] = byte(acc / 58)
			rem = acc % 58
		}
		out[i] = base58Alphabet[rem]
	}
	return string(out)
}
