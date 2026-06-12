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

// ListLocalModels returns .gguf files up to 3 levels deep in modelsDir.
// This covers flat layouts (file.gguf), HuggingFace-style (repo/file.gguf),
// and LMStudio-style (publisher/model-folder/file.gguf).
func ListLocalModels(modelsDir string) ([]LocalModel, error) {
	models := []LocalModel{}
	if err := walkGGUF(modelsDir, "", 0, &models); err != nil {
		if os.IsNotExist(err) {
			return models, nil
		}
		return nil, err
	}
	return models, nil
}

func walkGGUF(base, rel string, depth int, out *[]LocalModel) error {
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
	for _, e := range entries {
		var entryRel string
		if rel == "" {
			entryRel = e.Name()
		} else {
			entryRel = rel + "/" + e.Name()
		}
		if e.IsDir() {
			walkGGUF(base, entryRel, depth+1, out)
		} else if strings.HasSuffix(strings.ToLower(e.Name()), ".gguf") {
			fi, _ := e.Info()
			m := LocalModel{
				Name: entryRel,
				Path: filepath.Join(base, entryRel),
			}
			if fi != nil {
				m.Size = fi.Size()
			}
			*out = append(*out, m)
		}
	}
	return nil
}

// LocalModel describes a downloaded GGUF file on disk.
type LocalModel struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// DeleteLocalModel removes a model file from disk.
func DeleteLocalModel(modelsDir, name string) error {
	path := filepath.Join(modelsDir, name)
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
