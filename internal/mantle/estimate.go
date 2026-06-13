package mantle

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// ModelEstimate holds memory-size estimates derived from a model's GGUF
// metadata and its launch command.
type ModelEstimate struct {
	WeightsBytes  int64  `json:"weightsBytes"`
	KVCacheBytes  int64  `json:"kvCacheBytes"`
	TotalBytes    int64  `json:"totalBytes"`
	NCtx          int    `json:"nCtx"`
	NLayers       int    `json:"nLayers"`
	CacheTypeK    string `json:"cacheTypeK"`
	CacheTypeV    string `json:"cacheTypeV"`
	SlidingWindow int    `json:"slidingWindow"` // 0 when the model uses full attention
}

// swaPattern returns the period of the sliding-window-attention layer pattern
// for architectures known to interleave local (windowed) and global (full)
// attention layers. A return of N means 1 in every N layers uses full
// attention; the remaining N-1 only cache a sliding window. A return of 0 means
// the architecture uses full attention on every layer.
func swaPattern(arch string) int {
	switch strings.ToLower(arch) {
	case "gemma2":
		return 2 // alternating local / global
	case "gemma3", "gemma3n", "gemma3_text":
		return 6 // 5 local : 1 global
	case "cohere2":
		return 4 // 3 local : 1 global
	default:
		return 0
	}
}

// bytesPerElement maps a GGUF/llama.cpp KV cache type to its average storage
// cost per element, in bytes.
func bytesPerElement(cacheType string) float64 {
	switch strings.ToLower(cacheType) {
	case "f32":
		return 4
	case "f16", "bf16":
		return 2
	case "q8_0":
		return 34.0 / 32.0 // 1.0625
	case "q8_1":
		return 36.0 / 32.0
	case "q5_1":
		return 24.0 / 32.0 // 0.75
	case "q5_0":
		return 22.0 / 32.0 // 0.6875
	case "iq4_nl":
		return 18.0 / 32.0 // 0.5625
	case "q4_1":
		return 20.0 / 32.0 // 0.625
	case "q4_0":
		return 18.0 / 32.0 // 0.5625
	default:
		return 2 // assume f16
	}
}

// cmdParams holds the launch parameters relevant to size estimation.
type cmdParams struct {
	modelPath  string
	nCtx       int
	cacheTypeK string
	cacheTypeV string
}

// parseCmdParams extracts the model path, context size and cache types from a
// llama-server command string.
func parseCmdParams(cmd string) (cmdParams, error) {
	args, err := config.SanitizeCommand(cmd)
	if err != nil {
		return cmdParams{}, err
	}

	p := cmdParams{nCtx: 4096, cacheTypeK: "f16", cacheTypeV: "f16"}
	for i := 0; i < len(args); i++ {
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch args[i] {
		case "-m", "--model":
			p.modelPath = next()
		case "-c", "--ctx-size":
			if n, err := strconv.Atoi(next()); err == nil && n > 0 {
				p.nCtx = n
			}
		case "-ctk", "--cache-type-k":
			if v := next(); v != "" {
				p.cacheTypeK = v
			}
		case "-ctv", "--cache-type-v":
			if v := next(); v != "" {
				p.cacheTypeV = v
			}
		}
	}
	return p, nil
}

var splitFileRe = regexp.MustCompile(`-(\d{5})-of-(\d{5})\.gguf$`)

// weightsBytes returns the on-disk size of a model file, summing all parts of a
// multi-part (split) GGUF.
func weightsBytes(path string) int64 {
	m := splitFileRe.FindStringSubmatch(path)
	if m == nil {
		fi, err := os.Stat(path)
		if err != nil {
			return 0
		}
		return fi.Size()
	}

	total, _ := strconv.Atoi(m[2])
	prefix := path[:len(path)-len(m[0])]
	var sum int64
	for i := 1; i <= total; i++ {
		part := fmt.Sprintf("%s-%05d-of-%05d.gguf", prefix, i, total)
		if fi, err := os.Stat(part); err == nil {
			sum += fi.Size()
		}
	}
	return sum
}

// resolveModelPath turns a possibly-relative model path from a command into an
// absolute path that exists on disk.
func resolveModelPath(modelPath, modelsDir string) string {
	if modelPath == "" {
		return ""
	}
	if filepath.IsAbs(modelPath) {
		return modelPath
	}
	if joined := filepath.Join(modelsDir, modelPath); fileExists(joined) {
		return joined
	}
	return modelPath
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// --- metadata cache (keyed by path + mtime) ---

type cachedMeta struct {
	mtime int64
	meta  *GGUFMetadata
}

var (
	metaCacheMu sync.Mutex
	metaCache   = map[string]cachedMeta{}
)

func cachedGGUFMetadata(path string) (*GGUFMetadata, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	mtime := fi.ModTime().UnixNano()

	metaCacheMu.Lock()
	if c, ok := metaCache[path]; ok && c.mtime == mtime {
		metaCacheMu.Unlock()
		return c.meta, nil
	}
	metaCacheMu.Unlock()

	meta, err := ReadGGUFMetadata(path)
	if err != nil {
		return nil, err
	}

	metaCacheMu.Lock()
	metaCache[path] = cachedMeta{mtime: mtime, meta: meta}
	metaCacheMu.Unlock()
	return meta, nil
}

// EstimateModel computes weight and KV-cache size estimates for a model config.
func EstimateModel(cmd, modelsDir string) (*ModelEstimate, error) {
	params, err := parseCmdParams(cmd)
	if err != nil {
		return nil, err
	}
	if params.modelPath == "" {
		return nil, fmt.Errorf("no model path found in command")
	}

	path := resolveModelPath(params.modelPath, modelsDir)
	meta, err := cachedGGUFMetadata(path)
	if err != nil {
		return nil, err
	}

	arch, _ := meta.str("general.architecture")
	if arch == "" {
		return nil, fmt.Errorf("missing general.architecture")
	}

	nLayers, ok := meta.uint(arch + ".block_count")
	if !ok {
		return nil, fmt.Errorf("missing %s.block_count", arch)
	}
	nEmbd, _ := meta.uint(arch + ".embedding_length")
	nHead, _ := meta.uint(arch + ".attention.head_count")
	nHeadKV, ok := meta.uint(arch + ".attention.head_count_kv")
	if !ok {
		nHeadKV = nHead
	}

	// head_dim defaults to embedding_length / head_count when not specified.
	keyLen, ok := meta.uint(arch + ".attention.key_length")
	if !ok && nHead > 0 {
		keyLen = nEmbd / nHead
	}
	valLen, ok := meta.uint(arch + ".attention.value_length")
	if !ok {
		valLen = keyLen
	}

	nEmbdKGqa := keyLen * nHeadKV
	nEmbdVGqa := valLen * nHeadKV

	bytesK := bytesPerElement(params.cacheTypeK)
	bytesV := bytesPerElement(params.cacheTypeV)

	perLayerPerToken := float64(nEmbdKGqa)*bytesK + float64(nEmbdVGqa)*bytesV

	// Account for sliding-window attention. Models like Gemma 2/3 only cache a
	// fixed-size window on most layers, so the naive n_layers*n_ctx formula
	// massively overestimates their KV cache. This mirrors llama.cpp's default
	// allocation (windowed unless --swa-full is passed).
	slidingWindow, hasSWA := meta.uint(arch + ".attention.sliding_window")
	pattern := swaPattern(arch)

	var cacheTokens float64
	if hasSWA && slidingWindow > 0 && pattern > 0 {
		globalLayers := nLayers / uint64(pattern)
		swaLayers := nLayers - globalLayers
		effCtxSWA := uint64(params.nCtx)
		if slidingWindow < effCtxSWA {
			effCtxSWA = slidingWindow
		}
		cacheTokens = float64(globalLayers*uint64(params.nCtx) + swaLayers*effCtxSWA)
	} else {
		cacheTokens = float64(nLayers) * float64(params.nCtx)
	}

	kvBytes := cacheTokens * perLayerPerToken

	weights := weightsBytes(path)
	kv := int64(kvBytes)

	est := &ModelEstimate{
		WeightsBytes: weights,
		KVCacheBytes: kv,
		TotalBytes:   weights + kv,
		NCtx:         params.nCtx,
		NLayers:      int(nLayers),
		CacheTypeK:   params.cacheTypeK,
		CacheTypeV:   params.cacheTypeV,
	}
	if hasSWA && pattern > 0 {
		est.SlidingWindow = int(slidingWindow)
	}
	return est, nil
}
