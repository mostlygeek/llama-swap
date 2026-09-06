//go:build !windows

package server

import "io/fs"

// sharedWritable reports POSIX group/other write access on an already
// stat'ed capture directory.
func sharedWritable(fi fs.FileInfo) bool {
	return fi.Mode().Perm()&0o022 != 0
}
