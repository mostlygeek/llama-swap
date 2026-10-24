// Package sourcecode serves llama-swap's own source code over MCP, so the Help
// page's Docs agent can check what the knowledge base says against the code
// that implements it.
//
// The source is fetched on first use as a shallow git checkout of the release
// the binary was built from, kept on disk, and read from there on every call.
// Nothing touches the network until a tool is called. code__status compares
// the checkout with the repository, and code__update fetches it again. All of
// it needs the git command line tool.
package sourcecode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DefaultRepoURL is the canonical llama-swap repository.
const DefaultRepoURL = "https://github.com/mostlygeek/llama-swap"

// DefaultRef is checked out when the binary is not a tagged release.
const DefaultRef = "main"

// errNoGit means the git command line tool is not installed.
var errNoGit = errors.New("git is not installed on this server")

// commitHash matches a full git object id.
var commitHash = regexp.MustCompile(`^[0-9a-f]{40}$`)

// releaseTag matches the version string a release build carries, e.g. "v266".
// A `make` build carries `git describe` output such as "v266-3-gabc123" instead,
// which names a commit that may never have been pushed.
var releaseTag = regexp.MustCompile(`^v\d+$`)

// RefForVersion picks the ref matching a build's version: the release tag for
// a release build, otherwise DefaultRef.
func RefForVersion(version string) string {
	if releaseTag.MatchString(version) {
		return version
	}
	return DefaultRef
}

// DefaultCacheDir is where checkouts are kept between restarts.
func DefaultCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "llama-swap", "source")
	}
	return filepath.Join(os.TempDir(), "llama-swap-source")
}

// fetcher produces a checkout of one ref under cacheDir.
type fetcher struct {
	repoURL  string
	ref      string
	cacheDir string

	// lookGit finds the git binary. Tests replace it to simulate a server
	// without git.
	lookGit func() (string, error)
}

func (f *fetcher) git() (string, error) {
	path, err := f.lookGit()
	if err != nil {
		return "", errNoGit
	}
	return path, nil
}

// existing returns the checkout already on disk, if there is one.
func (f *fetcher) existing() (string, bool) {
	dir := filepath.Join(f.cacheDir, safeDirName(f.ref))
	info, err := os.Stat(dir)
	return dir, err == nil && info.IsDir()
}

// checkout returns the directory holding the ref's files. A checkout already
// on disk is used as is unless refresh is set, which clones it again.
func (f *fetcher) checkout(ctx context.Context, refresh bool) (string, error) {
	dir, exists := f.existing()
	if exists && !refresh {
		return dir, nil
	}
	gitPath, err := f.git()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(f.cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("creating cache directory: %w", err)
	}
	removeStaleFetches(f.cacheDir)

	// Clone into a scratch directory and rename it into place, so a directory
	// at the final path is always a complete checkout.
	tmp, err := os.MkdirTemp(f.cacheDir, ".fetch-")
	if err != nil {
		return "", fmt.Errorf("creating cache directory: %w", err)
	}
	defer os.RemoveAll(tmp)

	_, err = runGit(ctx, gitPath,
		"-c", "advice.detachedHead=false",
		// Keep the repository's line endings; Git for Windows defaults to CRLF.
		"-c", "core.autocrlf=false",
		"clone", "--quiet", "--depth", "1", "--single-branch", "--branch", f.ref,
		f.repoURL, tmp)
	if err != nil {
		return "", fmt.Errorf("git clone of %s at %s failed: %w", f.repoURL, f.ref, err)
	}

	if !refresh {
		// Another llama-swap sharing this cache may have won the race. Its
		// checkout is as good as this one, and it may be reading it.
		if err := os.Rename(tmp, dir); err != nil {
			if _, exists := f.existing(); exists {
				return dir, nil
			}
			return "", fmt.Errorf("moving checkout into place: %w", err)
		}
		return dir, nil
	}

	// Swap with two renames rather than deleting the old checkout first: a
	// call reading it meanwhile sees one tree or the other, never half of
	// one, and a failed swap puts the old one back. The "-old" name keeps the
	// ".fetch-" prefix, so removeStaleFetches clears it after a crash.
	old := tmp + "-old"
	if exists {
		if err := os.Rename(dir, old); err != nil {
			return "", fmt.Errorf("replacing old checkout: %w", err)
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		if exists {
			os.Rename(old, dir)
		}
		return "", fmt.Errorf("moving checkout into place: %w", err)
	}
	os.RemoveAll(old)
	return dir, nil
}

// localCommit returns the commit a checkout holds.
func (f *fetcher) localCommit(ctx context.Context, dir string) (string, error) {
	gitPath, err := f.git()
	if err != nil {
		return "", err
	}
	// Without its own .git, git would search parent directories and report
	// whatever repository the cache directory happens to sit in.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return "", fmt.Errorf("%s is not a git checkout", dir)
	}
	out, err := runGit(ctx, gitPath, "-C", dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if !commitHash.MatchString(out) {
		return "", fmt.Errorf("git rev-parse answered %q", out)
	}
	return out, nil
}

// remoteCommit asks the repository which commit the ref names now.
func (f *fetcher) remoteCommit(ctx context.Context) (string, error) {
	gitPath, err := f.git()
	if err != nil {
		return "", err
	}
	out, err := runGit(ctx, gitPath, "ls-remote", f.repoURL,
		"refs/heads/"+f.ref, "refs/tags/"+f.ref, "refs/tags/"+f.ref+"^{}")
	if err != nil {
		return "", err
	}

	// An annotated tag is listed twice; the "^{}" line is its commit.
	commit := ""
	for _, line := range strings.Split(out, "\n") {
		sha, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || !commitHash.MatchString(sha) {
			continue
		}
		if commit == "" || strings.HasSuffix(name, "^{}") {
			commit = sha
		}
	}
	if commit == "" {
		return "", fmt.Errorf("%s has no branch or tag named %s", f.repoURL, f.ref)
	}
	return commit, nil
}

// runGit runs git non-interactively and returns its trimmed standard output.
// A failure's error carries git's own message.
func runGit(ctx context.Context, gitPath string, args ...string) (string, error) {
	// The repository is public. An empty helper list stops Git Credential
	// Manager opening a login window nobody will see.
	cmd := exec.CommandContext(ctx, gitPath, append([]string{"-c", "credential.helper="}, args...)...)
	// Never wait on a credential prompt nobody can answer.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	// When ctx kills git, a git-remote-https child can hold the output pipe
	// open; stop waiting on it.
	cmd.WaitDelay = 5 * time.Second

	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", errors.New(msg)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// removeStaleFetches deletes scratch directories left by a process that exited
// mid-clone. Anything younger than fetchTimeout may still be in use.
func removeStaleFetches(cacheDir string) {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), ".fetch-") {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > fetchTimeout {
			os.RemoveAll(filepath.Join(cacheDir, e.Name()))
		}
	}
}

// safeDirName turns a ref into one path element.
func safeDirName(ref string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.', r == '_':
			return r
		default:
			return '_'
		}
	}, ref)
}
