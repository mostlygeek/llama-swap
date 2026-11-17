package sourcecode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"testing/fstest"
	"time"
)

// maxArchiveFileBytes bounds one file read back from an archive. The largest
// llama-swap source file is well under this.
const maxArchiveFileBytes = 8 << 20

// IsSourceFile reports whether a repository path belongs in the source the
// code__* tools serve: the same set as the Makefile's `src` target, which is
// Go files anywhere and TypeScript and Svelte files under ui/, without tests.
func IsSourceFile(name string) bool {
	switch {
	case strings.HasSuffix(name, "_test.go"), strings.HasSuffix(name, ".test.ts"):
		return false
	case strings.HasSuffix(name, ".go"):
		return true
	case strings.HasPrefix(name, "ui/") && (strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".svelte")):
		return true
	}
	return false
}

// skipDir reports whether a directory holds nothing that is part of the
// repository's own source: dependencies, build output, and the _src copy
// `make src` writes.
func skipDir(name string) bool {
	return name == "node_modules" || name == "build" || name == "ui_dist" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// WriteArchive writes the source files under root as a gzipped tar. The
// output depends only on the files' names and contents, so the same source
// always produces the same bytes.
func WriteArchive(w io.Writer, root fs.FS) error {
	var names []string
	err := fs.WalkDir(root, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name != "." && skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && IsSourceFile(name) {
			names = append(names, name)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("no source files found")
	}
	sort.Strings(names)

	gz, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(gz)
	epoch := time.Unix(0, 0).UTC()
	for _, name := range names {
		data, err := fs.ReadFile(root, name)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(data)),
			ModTime:  epoch,
			Typeflag: tar.TypeReg,
			Format:   tar.FormatPAX,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// ReadArchive loads an archive written by WriteArchive into memory. The
// llama-swap source is about 2 MB uncompressed.
func ReadArchive(data []byte) (fs.FS, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("opening gzip: %w", err)
	}
	defer gz.Close()

	files := fstest.MapFS{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Clean(hdr.Name)
		if !fs.ValidPath(name) {
			return nil, fmt.Errorf("invalid path %q in archive", hdr.Name)
		}
		if hdr.Size > maxArchiveFileBytes {
			return nil, fmt.Errorf("%s is %d bytes, over the %d byte limit", name, hdr.Size, maxArchiveFileBytes)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		files[name] = &fstest.MapFile{Data: content, Mode: 0o444}
	}
	return files, nil
}
