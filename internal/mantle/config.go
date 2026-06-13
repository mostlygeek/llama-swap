package mantle

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// ListBackends returns the directories in the backends folder.
// Each directory represents a compiled backend build.
func ListBackends(backendsDir string) ([]BackendEntry, error) {
	entries, err := os.ReadDir(backendsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []BackendEntry{}, nil
		}
		return nil, err
	}

	backends := []BackendEntry{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		binPath := filepath.Join(backendsDir, e.Name(), "llama-server")
		if _, err := os.Stat(binPath); err != nil {
			continue
		}
		fi, _ := os.Stat(binPath)
		entry := BackendEntry{
			Name:   e.Name(),
			Path:   binPath,
			TaskID: extractTaskID(e.Name()),
		}
		if fi != nil {
			entry.Size = fi.Size()
		}
		backends = append(backends, entry)
	}
	return backends, nil
}

// BackendEntry describes a compiled backend.
type BackendEntry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	TaskID string `json:"taskID,omitempty"`
}

// DeleteBackend removes a compiled backend directory.
func DeleteBackend(backendsDir, name string) error {
	path := filepath.Join(backendsDir, name)
	return os.RemoveAll(path)
}

// ListLocalModels returns downloaded models in modelsDir, up to 3 levels deep.
// It lists individual .gguf files (flat, HuggingFace-style repo/file.gguf, and
// LMStudio-style publisher/model-folder/file.gguf layouts) as well as non-GGUF
// model repos — directories holding safetensors / a whisper .bin / a
// config.json — which are surfaced as a single entry pointing at the directory.
func ListLocalModels(modelsDir string) ([]LocalModel, error) {
	models := []LocalModel{}
	if err := walkModels(modelsDir, "", 0, &models); err != nil {
		if os.IsNotExist(err) {
			return models, nil
		}
		return nil, err
	}
	return models, nil
}

func walkModels(base, rel string, depth int, out *[]LocalModel) error {
	if depth > 2 {
		return nil
	}
	dir := base
	if rel != "" {
		dir = filepath.Join(base, rel)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	// If this directory is itself a non-GGUF model repo, emit one entry for it
	// and stop descending — its individual weight files are not separately
	// loadable.
	if rel != "" {
		if kind := repoKind(entries); kind != "" {
			*out = append(*out, LocalModel{
				Name: rel,
				Path: filepath.Join(base, rel),
				Size: dirSize(dir),
				Kind: kind,
			})
			return nil
		}
	}

	for _, e := range entries {
		var entryRel string
		if rel == "" {
			entryRel = e.Name()
		} else {
			entryRel = rel + "/" + e.Name()
		}
		if e.IsDir() {
			walkModels(base, entryRel, depth+1, out)
		} else if strings.HasSuffix(strings.ToLower(e.Name()), ".gguf") {
			fi, _ := e.Info()
			m := LocalModel{
				Name: entryRel,
				Path: filepath.Join(base, entryRel),
				Kind: "gguf",
			}
			if fi != nil {
				m.Size = fi.Size()
			}
			*out = append(*out, m)
		}
	}
	return nil
}

// repoKind classifies a directory's contents as a non-GGUF model repo, or
// returns "" when it is not one (e.g. only contains .gguf files or sub-dirs).
func repoKind(entries []os.DirEntry) string {
	var hasSafetensors, hasConfig, hasBin, hasGGUF bool
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		switch {
		case strings.HasSuffix(name, ".safetensors"):
			hasSafetensors = true
		case strings.HasSuffix(name, ".gguf"):
			hasGGUF = true
		case strings.HasSuffix(name, ".bin"):
			hasBin = true
		case name == "config.json":
			hasConfig = true
		}
	}
	switch {
	case hasSafetensors:
		return "safetensors"
	case hasBin && !hasGGUF:
		return "whisper"
	case hasConfig && !hasGGUF:
		return "repo"
	default:
		return ""
	}
}

func dirSize(dir string) int64 {
	var total int64
	filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += fi.Size()
		}
		return nil
	})
	return total
}

// LocalModel describes a downloaded model on disk: either a single GGUF file or
// a non-GGUF model repo directory.
type LocalModel struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	Kind string `json:"kind"` // gguf | safetensors | whisper | repo
}

// DeleteLocalModel removes a model file, or a model repo directory, from disk.
func DeleteLocalModel(modelsDir, name string) error {
	path := filepath.Join(modelsDir, name)
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

func extractTaskID(name string) string {
	if len(name) > 6 && name[:6] == "build-" {
		return name[6:]
	}
	return ""
}

func isSafeBackendName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '.', '_', '-':
			continue
		default:
			return false
		}
	}
	return true
}
