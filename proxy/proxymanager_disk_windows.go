//go:build windows

package proxy

import "golang.org/x/sys/windows"

func diskStorageStats(dir string) (map[string]any, bool) {
	pathPtr, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return nil, false
	}
	var freeBytesAvailable, totalBytes, totalFreeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &freeBytesAvailable, &totalBytes, &totalFreeBytes); err != nil {
		return nil, false
	}
	return map[string]any{
		"total_bytes":     totalBytes,
		"available_bytes": freeBytesAvailable,
		"used_bytes":      totalBytes - freeBytesAvailable,
	}, true
}
