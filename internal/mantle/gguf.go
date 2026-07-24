package mantle

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
)

// GGUF metadata value types (see ggml/gguf spec).
const (
	ggufTypeUint8 uint32 = iota
	ggufTypeInt8
	ggufTypeUint16
	ggufTypeInt16
	ggufTypeUint32
	ggufTypeInt32
	ggufTypeFloat32
	ggufTypeBool
	ggufTypeString
	ggufTypeArray
	ggufTypeUint64
	ggufTypeInt64
	ggufTypeFloat64
)

const ggufMagic uint32 = 0x46554747 // "GGUF" little-endian

// GGUFMetadata holds the key/value metadata block of a GGUF file. Tensor data
// is intentionally not read — only the header is needed for size estimation.
type GGUFMetadata struct {
	Version uint32
	KV      map[string]any
}

// ReadGGUFMetadata parses just the metadata KV block of a GGUF file.
func ReadGGUFMetadata(path string) (*GGUFMetadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := bufio.NewReader(f)

	var magic uint32
	if err := binary.Read(r, binary.LittleEndian, &magic); err != nil {
		return nil, fmt.Errorf("read magic: %w", err)
	}
	if magic != ggufMagic {
		return nil, fmt.Errorf("not a GGUF file (bad magic 0x%08x)", magic)
	}

	var version uint32
	if err := binary.Read(r, binary.LittleEndian, &version); err != nil {
		return nil, fmt.Errorf("read version: %w", err)
	}
	if version != 2 && version != 3 {
		return nil, fmt.Errorf("unsupported GGUF version %d", version)
	}

	// tensor_count and metadata_kv_count are uint64 in v2/v3.
	if _, err := readUint64(r); err != nil { // tensor_count (unused)
		return nil, fmt.Errorf("read tensor_count: %w", err)
	}
	kvCount, err := readUint64(r)
	if err != nil {
		return nil, fmt.Errorf("read kv_count: %w", err)
	}

	kv := make(map[string]any, kvCount)
	for i := uint64(0); i < kvCount; i++ {
		key, err := readGGUFString(r)
		if err != nil {
			return nil, fmt.Errorf("read kv key #%d: %w", i, err)
		}
		val, err := readGGUFValue(r)
		if err != nil {
			return nil, fmt.Errorf("read kv value for %q: %w", key, err)
		}
		kv[key] = val
	}

	return &GGUFMetadata{Version: version, KV: kv}, nil
}

func readUint64(r io.Reader) (uint64, error) {
	var v uint64
	err := binary.Read(r, binary.LittleEndian, &v)
	return v, err
}

func readGGUFString(r io.Reader) (string, error) {
	n, err := readUint64(r)
	if err != nil {
		return "", err
	}
	if n > 64*1024*1024 {
		return "", fmt.Errorf("string length %d too large", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func readGGUFValue(r io.Reader) (any, error) {
	var t uint32
	if err := binary.Read(r, binary.LittleEndian, &t); err != nil {
		return nil, err
	}
	return readGGUFTypedValue(r, t)
}

func readGGUFTypedValue(r io.Reader, t uint32) (any, error) {
	switch t {
	case ggufTypeUint8:
		var v uint8
		err := binary.Read(r, binary.LittleEndian, &v)
		return uint64(v), err
	case ggufTypeInt8:
		var v int8
		err := binary.Read(r, binary.LittleEndian, &v)
		return int64(v), err
	case ggufTypeUint16:
		var v uint16
		err := binary.Read(r, binary.LittleEndian, &v)
		return uint64(v), err
	case ggufTypeInt16:
		var v int16
		err := binary.Read(r, binary.LittleEndian, &v)
		return int64(v), err
	case ggufTypeUint32:
		var v uint32
		err := binary.Read(r, binary.LittleEndian, &v)
		return uint64(v), err
	case ggufTypeInt32:
		var v int32
		err := binary.Read(r, binary.LittleEndian, &v)
		return int64(v), err
	case ggufTypeFloat32:
		var v float32
		err := binary.Read(r, binary.LittleEndian, &v)
		return float64(v), err
	case ggufTypeBool:
		var v uint8
		err := binary.Read(r, binary.LittleEndian, &v)
		return v != 0, err
	case ggufTypeString:
		return readGGUFString(r)
	case ggufTypeUint64:
		var v uint64
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err
	case ggufTypeInt64:
		var v int64
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err
	case ggufTypeFloat64:
		var v float64
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err
	case ggufTypeArray:
		var elemType uint32
		if err := binary.Read(r, binary.LittleEndian, &elemType); err != nil {
			return nil, err
		}
		n, err := readUint64(r)
		if err != nil {
			return nil, err
		}
		// We don't need array values for size estimation, but we must consume
		// the bytes so subsequent keys parse correctly.
		arr := make([]any, 0)
		for i := uint64(0); i < n; i++ {
			v, err := readGGUFTypedValue(r, elemType)
			if err != nil {
				return nil, err
			}
			if i < 16 { // keep a few values; discard the rest
				arr = append(arr, v)
			}
		}
		return arr, nil
	default:
		return nil, fmt.Errorf("unknown GGUF value type %d", t)
	}
}

// uint returns a metadata value as a uint64 regardless of its concrete integer
// type. The second return is false if the key is missing or non-numeric.
func (m *GGUFMetadata) uint(key string) (uint64, bool) {
	v, ok := m.KV[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case uint64:
		return n, true
	case int64:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	case float64:
		if n < 0 || math.IsNaN(n) {
			return 0, false
		}
		return uint64(n), true
	case []any:
		// Some models store per-layer values (e.g. head_count_kv) as an array.
		// Use the first element as a representative value.
		if len(n) == 0 {
			return 0, false
		}
		sub := &GGUFMetadata{KV: map[string]any{key: n[0]}}
		return sub.uint(key)
	default:
		return 0, false
	}
}

// str returns a metadata value as a string.
func (m *GGUFMetadata) str(key string) (string, bool) {
	v, ok := m.KV[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
