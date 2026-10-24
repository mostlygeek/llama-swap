package sourcecode

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/mcptools"
)

// remoteCheckTimeout bounds one question to the repository about the ref's
// current commit.
const remoteCheckTimeout = 15 * time.Second

// buildCommitPattern matches the commit a build reports: a short hash, with
// "+" when `make` built it from a working tree with uncommitted changes.
var buildCommitPattern = regexp.MustCompile(`^([0-9a-f]{7,40})(\+?)$`)

func (p *Provider) status(ctx context.Context) mcptools.Result {
	p.mu.Lock()
	loading, lastErr := p.loading != nil, p.err
	p.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "this server: llama-swap %s, built from commit %s\n", orUnknown(p.version), orUnknown(p.buildCommit))

	dir, exists := p.fetcher.existing()
	if _, err := p.fetcher.git(); err != nil {
		b.WriteString("git: not installed\n\n" + noGitAdvice + "\n")
		if exists {
			fmt.Fprintf(&b, "A checkout downloaded earlier is still at %s and can be searched, but not checked or updated.\n", dir)
		}
		return mcptools.Result{Content: b.String()}
	}

	local, localErr := "", error(nil)
	if exists {
		local, localErr = p.fetcher.localCommit(ctx, dir)
		fmt.Fprintf(&b, "checkout: %s at commit %s\nlocation: %s\n", p.ref, shortCommit(local), dir)
	} else {
		b.WriteString("checkout: none\n")
	}
	remote, remoteErr := p.remoteCommit(ctx)
	if remoteErr == nil {
		fmt.Fprintf(&b, "repository: %s is at commit %s\n", p.ref, shortCommit(remote))
	} else {
		fmt.Fprintf(&b, "repository: could not check: %v\n", remoteErr)
	}
	b.WriteString("\n")

	update := mcptools.QualifyName(p.ID(), "update")
	switch {
	case loading:
		fmt.Fprintf(&b, "A download is running now. Call %s again in a few seconds.\n", mcptools.QualifyName(p.ID(), "status"))
	case !exists && lastErr != nil:
		fmt.Fprintf(&b, "Nothing is downloaded: the last attempt failed with %v. Call %s to try again.\n", lastErr, update)
	case !exists:
		fmt.Fprintf(&b, "Nothing is downloaded yet. Call %s to fetch it; the first grep, read, find or ls call also fetches it.\n", update)
	case localErr != nil:
		fmt.Fprintf(&b, "The checkout's commit could not be read (%v). Call %s with force=true to download it again.\n", localErr, update)
	case remoteErr != nil:
		b.WriteString("The checkout could not be compared with the repository. It is still usable as it is.\n")
	case local != remote:
		fmt.Fprintf(&b, "The checkout is out of date: %s has moved from %s to %s. Call %s to fetch it.\n",
			p.ref, shortCommit(local), shortCommit(remote), update)
	default:
		b.WriteString("The checkout is up to date with the repository.\n")
	}
	if local != "" {
		b.WriteString(p.compareBuild(local))
	}

	return mcptools.Result{Content: b.String()}
}

// compareBuild says whether a checkout's commit is the one this server was
// built from, or "" when the build does not say which commit that was.
func (p *Provider) compareBuild(commit string) string {
	m := buildCommitPattern.FindStringSubmatch(p.buildCommit)
	// "0" is the version of a plain `go build`, whose commit is a placeholder.
	if m == nil || p.version == "" || p.version == "0" {
		return ""
	}
	if !strings.HasPrefix(commit, m[1]) {
		return fmt.Sprintf("This server was built from commit %s, not %s, so the running code may differ from the checkout.\n",
			m[1], shortCommit(commit))
	}
	if m[2] == "+" {
		return "The checkout is the commit this server was built from, but the build had uncommitted changes, so the running code may differ.\n"
	}
	return "The checkout is the commit this server was built from.\n"
}

func (p *Provider) update(ctx context.Context, force bool) mcptools.Result {
	if _, err := p.fetcher.git(); err != nil {
		return mcptools.Result{IsError: true, Content: noGitAdvice}
	}
	stillRunning := mcptools.Result{IsError: true, Content: fmt.Sprintf(
		"The download is still running. Call %s in a few seconds to see how it went.", mcptools.QualifyName(p.ID(), "status"))}

	// A load already running may be all that is needed.
	p.mu.Lock()
	running := p.loading
	p.mu.Unlock()
	if running != nil && wait(ctx, running) != nil {
		return stillRunning
	}

	dir, exists := p.fetcher.existing()
	before := ""
	if exists {
		before, _ = p.fetcher.localCommit(ctx, dir)
	}
	if exists && !force {
		remote, err := p.remoteCommit(ctx)
		if err != nil {
			return mcptools.Result{IsError: true, Content: fmt.Sprintf(
				"Could not check the repository for updates: %v. The checkout at commit %s is still usable; "+
					"set force=true to download it again anyway.", err, shortCommit(before))}
		}
		if before == remote {
			if _, err := p.ensure(ctx); err != nil {
				return mcptools.Result{IsError: true, Content: fmt.Sprintf("The checkout is current but could not be opened: %v.", err)}
			}
			return mcptools.Result{Content: fmt.Sprintf("Already up to date: %s at commit %s.", p.ref, shortCommit(remote))}
		}
	}

	p.mu.Lock()
	done := p.startLoad(true)
	p.mu.Unlock()
	if err := wait(ctx, done); errors.Is(err, errLoading) {
		return stillRunning
	}

	p.mu.Lock()
	after, loaded, err := p.commit, p.index != nil, p.err
	p.mu.Unlock()
	if err != nil {
		content := fmt.Sprintf("The download failed: %v.", err)
		if loaded {
			content += " The previous checkout is still in use."
		}
		return mcptools.Result{IsError: true, Content: content}
	}

	switch {
	case !exists:
		return mcptools.Result{Content: fmt.Sprintf("Downloaded %s at commit %s.", p.ref, shortCommit(after))}
	case before != after:
		return mcptools.Result{Content: fmt.Sprintf("Updated %s from commit %s to %s.", p.ref, shortCommit(before), shortCommit(after))}
	default:
		return mcptools.Result{Content: fmt.Sprintf("Downloaded %s again; it is still at commit %s.", p.ref, shortCommit(after))}
	}
}

func (p *Provider) remoteCommit(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, remoteCheckTimeout)
	defer cancel()
	return p.fetcher.remoteCommit(ctx)
}

func shortCommit(commit string) string {
	if commit == "" {
		return "unknown"
	}
	return commit[:min(len(commit), 12)]
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
