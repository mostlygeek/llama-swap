package mantle

import (
	"os"
	"path/filepath"
)

// ListBackends returns the directories in the backends folder.
// Each directory represents a compiled backend build.
func ListBackends(backendsDir string) ([]BackendEntry, error) {
	entries, err := os.ReadDir(backendsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var backends []BackendEntry
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
			Name:    e.Name(),
			Path:    binPath,
			TaskID:  extractTaskID(e.Name()),
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
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	TaskID  string `json:"taskID,omitempty"`
}

// DeleteBackend removes a compiled backend directory.
func DeleteBackend(backendsDir, name string) error {
	path := filepath.Join(backendsDir, name)
	return os.RemoveAll(path)
}

// ListLocalModels returns the .gguf files in the models directory.
func ListLocalModels(modelsDir string) ([]LocalModel, error) {
	entries, err := os.ReadDir(modelsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var models []LocalModel
	for _, e := range entries {
		if !e.IsDir() {
			if len(e.Name()) > 5 && e.Name()[len(e.Name())-5:] == ".gguf" {
				fi, _ := e.Info()
				m := LocalModel{
					Name: e.Name(),
					Path: filepath.Join(modelsDir, e.Name()),
				}
				if fi != nil {
					m.Size = fi.Size()
				}
				models = append(models, m)
			}
			continue
		}
		// Check inside subdirectory
		subEntries, _ := os.ReadDir(filepath.Join(modelsDir, e.Name()))
		for _, se := range subEntries {
			if !se.IsDir() && len(se.Name()) > 5 && se.Name()[len(se.Name())-5:] == ".gguf" {
				fi, _ := se.Info()
				m := LocalModel{
					Name: e.Name() + "/" + se.Name(),
					Path: filepath.Join(modelsDir, e.Name(), se.Name()),
				}
				if fi != nil {
					m.Size = fi.Size()
				}
				models = append(models, m)
			}
		}
	}
	return models, nil
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
