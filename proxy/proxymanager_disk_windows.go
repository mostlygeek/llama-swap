//go:build windows

package proxy

func diskStorageStats(dir string) (map[string]any, bool) {
	return nil, false
}
