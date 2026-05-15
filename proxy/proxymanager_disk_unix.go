//go:build !windows

package proxy

import "syscall"

func diskStorageStats(dir string) (map[string]any, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return nil, false
	}
	bs := uint64(st.Bsize)
	return map[string]any{
		"total_bytes":     st.Blocks * bs,
		"available_bytes": st.Bavail * bs,
		"used_bytes":      (st.Blocks - st.Bfree) * bs,
	}, true
}
