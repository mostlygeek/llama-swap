package configwatcher

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DirWatcher polls a directory for changes to its set of *.yml / *.yaml files.
// It fires OnChange when a file is added, removed, or has its mod time/size
// change. Like Watcher it is poll-based so it works in Docker bind-mounts and
// k8s ConfigMap projections where inotify is unreliable.
//
// The baseline poll establishes initial state and does not fire OnChange.
type DirWatcher struct {
	Path     string
	Interval time.Duration
	OnChange func()
}

// dirSnapshot is an ordered map of file name -> file state. The ordering is
// derived from sorted filenames so two snapshots compare deterministically
// regardless of readdir order. exists reflects whether the directory was
// readable at scan time; a missing directory yields exists=false.
type dirSnapshot struct {
	exists bool
	names  []string
	states map[string]snapshot
}

func newDirSnapshot() dirSnapshot {
	return dirSnapshot{states: make(map[string]snapshot)}
}

// equal reports whether two snapshots describe the same file set and per-file
// state. A missing directory (exists=false) is treated as equal to any other
// missing directory regardless of cached names.
func (s dirSnapshot) equal(other dirSnapshot) bool {
	if !s.exists && !other.exists {
		return true
	}
	if s.exists != other.exists {
		return false
	}
	if len(s.names) != len(other.names) {
		return false
	}
	for i, n := range s.names {
		if other.names[i] != n {
			return false
		}
	}
	for _, n := range s.names {
		a, b := s.states[n], other.states[n]
		if a.exists != b.exists || a.size != b.size || !a.modTime.Equal(b.modTime) {
			return false
		}
	}
	return true
}

// Run blocks until ctx is canceled. It polls Path on Interval and invokes
// OnChange whenever the directory's YAML file set changes.
//
// Policy mirrors the single-file Watcher: disappearance (directory missing or
// empty) is treated as a transient rename-style write and stays quiet; the
// transition back to present-with-content fires OnChange.
func (w *DirWatcher) Run(ctx context.Context) {
	interval := w.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}

	prev := scanDir(w.Path)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur := scanDir(w.Path)
			// Suppress transitions involving an empty or missing directory —
			// these are treated as transient rename-style writes, mirroring
			// the single-file Watcher. Only present-with-content →
			// present-with-content (changed) or no-content →
			// present-with-content fires OnChange.
			prevHasContent := prev.exists && len(prev.names) > 0
			curHasContent := cur.exists && len(cur.names) > 0
			if curHasContent && (!prevHasContent || !prev.equal(cur)) && w.OnChange != nil {
				w.OnChange()
			}
			prev = cur
		}
	}
}

// scanDir returns a snapshot of the *.yml/*.yaml files in dir. If the
// directory cannot be read (missing, permission denied) the snapshot reports
// exists=false; the next successful scan will detect the recovery and fire
// OnChange.
func scanDir(dir string) dirSnapshot {
	snap := newDirSnapshot()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return snap // exists=false
	}
	snap.exists = true
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			// File disappeared between ReadDir and Stat; skip it — the
			// next poll will observe the removal cleanly.
			continue
		}
		snap.names = append(snap.names, name)
		snap.states[name] = snapshot{
			exists:  true,
			modTime: fi.ModTime(),
			size:    fi.Size(),
		}
	}
	sort.Strings(snap.names)
	return snap
}
