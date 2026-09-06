//go:build windows

package server

import "io/fs"

// sharedWritable always reports false: Go synthesizes 0777 for every Windows
// directory because NTFS ACLs have no POSIX mode bits, so the check would warn
// on all dirs. Detecting world-writable ACLs would need an ACL API beyond the
// best-effort advisory this warning provides.
func sharedWritable(fi fs.FileInfo) bool {
	return false
}
