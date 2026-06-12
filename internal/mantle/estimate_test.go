package mantle

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// writeGGUFString writes a GGUF string (uint64 length + bytes).
func writeGGUFString(buf *bytes.Buffer, s string) {
	binary.Write(buf, binary.LittleEndian, uint64(len(s)))
	buf.WriteString(s)
}

// writeGGUFKVString writes a string-typed metadata entry.
func writeGGUFKVString(buf *bytes.Buffer, key, val string) {
	writeGGUFString(buf, key)
	binary.Write(buf, binary.LittleEndian, ggufTypeString)
	writeGGUFString(buf, val)
}

// writeGGUFKVUint32 writes a uint32-typed metadata entry.
func writeGGUFKVUint32(buf *bytes.Buffer, key string, val uint32) {
	writeGGUFString(buf, key)
	binary.Write(buf, binary.LittleEndian, ggufTypeUint32)
	binary.Write(buf, binary.LittleEndian, val)
}

// makeTestGGUF writes a minimal GGUF file with a llama-style metadata block.
func makeTestGGUF(t *testing.T, path string) {
	t.Helper()

	var meta bytes.Buffer
	writeGGUFKVString(&meta, "general.architecture", "llama")
	writeGGUFKVUint32(&meta, "llama.block_count", 4)
	writeGGUFKVUint32(&meta, "llama.embedding_length", 64)
	writeGGUFKVUint32(&meta, "llama.attention.head_count", 8)
	writeGGUFKVUint32(&meta, "llama.attention.head_count_kv", 2)

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, ggufMagic)
	binary.Write(&buf, binary.LittleEndian, uint32(3)) // version
	binary.Write(&buf, binary.LittleEndian, uint64(0)) // tensor_count
	binary.Write(&buf, binary.LittleEndian, uint64(5)) // metadata_kv_count
	buf.Write(meta.Bytes())

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write gguf: %v", err)
	}
}

func TestEstimateModel_KVCacheFormula(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model.gguf")
	makeTestGGUF(t, path)

	// head_dim = embedding_length(64) / head_count(8) = 8
	// n_embd_k_gqa = n_embd_v_gqa = head_dim(8) * head_count_kv(2) = 16
	cmd := "llama-server -m " + path + " -c 8192 -ctk q8_0 -ctv q4_0"
	est, err := EstimateModel(cmd, dir)
	if err != nil {
		t.Fatalf("EstimateModel: %v", err)
	}

	if est.NCtx != 8192 {
		t.Errorf("NCtx = %d, want 8192", est.NCtx)
	}
	if est.NLayers != 4 {
		t.Errorf("NLayers = %d, want 4", est.NLayers)
	}

	// kv = layers(4) * ctx(8192) * (16*q8_0 + 16*q4_0)
	//    = 4 * 8192 * (16*1.0625 + 16*0.5625) = 4 * 8192 * 26 = 851968
	const wantKV = 851968
	if est.KVCacheBytes != wantKV {
		t.Errorf("KVCacheBytes = %d, want %d", est.KVCacheBytes, wantKV)
	}

	if est.TotalBytes != est.WeightsBytes+est.KVCacheBytes {
		t.Errorf("TotalBytes = %d, want %d", est.TotalBytes, est.WeightsBytes+est.KVCacheBytes)
	}
}

func TestEstimateModel_DefaultCtxAndCacheType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model.gguf")
	makeTestGGUF(t, path)

	est, err := EstimateModel("llama-server --model "+path, dir)
	if err != nil {
		t.Fatalf("EstimateModel: %v", err)
	}

	if est.NCtx != 4096 {
		t.Errorf("NCtx = %d, want default 4096", est.NCtx)
	}
	if est.CacheTypeK != "f16" || est.CacheTypeV != "f16" {
		t.Errorf("cache types = %s/%s, want f16/f16", est.CacheTypeK, est.CacheTypeV)
	}

	// kv = 4 * 4096 * (16*2 + 16*2) = 4 * 4096 * 64 = 1048576
	const wantKV = 1048576
	if est.KVCacheBytes != wantKV {
		t.Errorf("KVCacheBytes = %d, want %d", est.KVCacheBytes, wantKV)
	}
}
