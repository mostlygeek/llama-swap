package sourcecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/internal/mcptools"
)

const (
	// fetchTimeout bounds one checkout. A shallow clone takes a second or two.
	fetchTimeout = 3 * time.Minute

	// callWait is how long a tool call waits on a checkout in progress before
	// telling the model to try again.
	callWait = 30 * time.Second

	// retryAfter stops a host with no network access from trying to fetch on
	// every call.
	retryAfter = time.Minute
)

// noGitAdvice is what every code__* tool says when git is missing, so the
// agent can tell the user what to do about it.
const noGitAdvice = "git is not installed on the server running llama-swap. The code__* tools need the git " +
	"command line tool to download and update llama-swap's source code. Tell the user to install git."

// errLoading means a checkout is still being fetched.
var errLoading = errors.New("still downloading")

// Options configures a Provider.
type Options struct {
	// RepoURL defaults to DefaultRepoURL.
	RepoURL string
	// Ref is the tag or branch to check out. Defaults to DefaultRef.
	Ref string
	// CacheDir holds checkouts. Defaults to DefaultCacheDir().
	CacheDir string
	// Version and Commit describe the running build, so code__status can say
	// whether the checkout matches it.
	Version string
	Commit  string
	// Logf, when set, reports fetches.
	Logf func(format string, args ...any)
}

// Provider serves llama-swap's source code as the "code" tool namespace.
//
// The checkout is fetched by the first tool call, not at construction, so a
// server that never uses these tools never touches the network. It is built
// once per process and shared across config reloads, which is why Shutdown
// leaves it alone.
type Provider struct {
	ref         string
	repoURL     string
	version     string
	buildCommit string
	fetcher     *fetcher
	logf        func(format string, args ...any)
	now         func() time.Time

	mu      sync.Mutex
	index   *Index
	commit  string        // what index holds; "" if git could not say
	loading chan struct{} // closed when the running load finishes
	err     error
	errAt   time.Time
}

var _ mcptools.Provider = (*Provider)(nil)

// NewProvider builds the provider. It does no I/O.
func NewProvider(opts Options) *Provider {
	if opts.RepoURL == "" {
		opts.RepoURL = DefaultRepoURL
	}
	if opts.Ref == "" {
		opts.Ref = DefaultRef
	}
	if opts.CacheDir == "" {
		opts.CacheDir = DefaultCacheDir()
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Provider{
		ref:         opts.Ref,
		repoURL:     opts.RepoURL,
		version:     opts.Version,
		buildCommit: opts.Commit,
		fetcher: &fetcher{
			repoURL:  opts.RepoURL,
			ref:      opts.Ref,
			cacheDir: opts.CacheDir,
			lookGit:  func() (string, error) { return exec.LookPath("git") },
		},
		logf: logf,
		now:  time.Now,
	}
}

func (p *Provider) ID() string { return "code" }

// Shutdown is a no-op: the provider outlives the server that registered it.
func (p *Provider) Shutdown(time.Duration) error { return nil }

// ensure returns the index, starting a load or waiting on one in progress. A
// checkout already on disk is used as is; code__update is what refreshes it.
func (p *Provider) ensure(ctx context.Context) (*Index, error) {
	p.mu.Lock()
	if p.index != nil {
		idx := p.index
		p.mu.Unlock()
		return idx, nil
	}
	if p.loading == nil && p.err != nil && p.now().Sub(p.errAt) < retryAfter {
		err := p.err
		p.mu.Unlock()
		return nil, err
	}
	done := p.startLoad(false)
	p.mu.Unlock()

	if err := wait(ctx, done); err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.index != nil {
		return p.index, nil
	}
	return nil, p.err
}

// startLoad starts a load unless one is running, and returns the channel
// closed when the running one finishes. p.mu must be held.
func (p *Provider) startLoad(refresh bool) chan struct{} {
	if p.loading == nil {
		p.loading = make(chan struct{})
		go p.load(p.loading, refresh)
	}
	return p.loading
}

// wait blocks until done closes, the call is cancelled, or callWait passes.
func wait(ctx context.Context, done chan struct{}) error {
	timer := time.NewTimer(callWait)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errLoading
	case <-timer.C:
		return errLoading
	}
}

// load runs detached from any one tool call, so a call that gives up waiting
// does not abandon the fetch.
func (p *Provider) load(done chan struct{}, refresh bool) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()

	started := p.now()
	if _, exists := p.fetcher.existing(); refresh || !exists {
		p.logf("code tools: fetching %s at %s", p.repoURL, p.ref)
	}

	var idx *Index
	var commit string
	dir, err := p.fetcher.checkout(ctx, refresh)
	if err == nil {
		idx, err = LoadIndex(dir)
		commit, _ = p.fetcher.localCommit(ctx, dir)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.logf("code tools: fetching source failed: %v", err)
		p.err, p.errAt = err, p.now()
	} else {
		p.index, p.commit, p.err = idx, commit, nil
		p.logf("code tools: source at commit %s ready in %s after %s",
			shortCommit(commit), dir, p.now().Sub(started).Round(time.Millisecond))
	}
	p.loading = nil
	close(done)
}

func codeAnnotations() *mcptools.Annotations {
	// The checkout is pinned to one ref, so the same call gives the same answer.
	return &mcptools.Annotations{ReadOnlyHint: true, IdempotentHint: true}
}

// Tools lists the source code tools. Names are local; the registry adds the
// "code" namespace.
func (p *Provider) Tools(context.Context) ([]mcptools.Tool, error) {
	update := mcptools.QualifyName(p.ID(), "update")
	tools := []mcptools.Tool{
		{
			Name:  "status",
			Title: "Check the llama-swap source checkout",
			Description: "Check whether llama-swap's source code is downloaded on this server, which commit it holds, " +
				"and whether it is out of date: against the repository on GitHub, and against the build this server runs. " +
				"Call it when a code__* tool reports the source missing or a fetch failure, or before relying on the code for a detail " +
				"that may have changed recently. It says when to call " + update + ".",
			// Read-only, but not idempotent: the repository moves.
			Annotations: &mcptools.Annotations{ReadOnlyHint: true},
			Tags:        []string{"code", "source", "status"},
			InputSchema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		},
		{
			Name:  "update",
			Title: "Fetch or update the llama-swap source",
			Description: "Download llama-swap's source code at " + p.ref + ", or download it again when " +
				mcptools.QualifyName(p.ID(), "status") + " reports it out of date. Does nothing when the checkout already " +
				"matches the repository, unless force is set.",
			// It replaces the local checkout, so it is not read-only; calling it
			// twice leaves the same checkout as calling it once.
			Annotations: &mcptools.Annotations{IdempotentHint: true},
			Tags:        []string{"code", "source", "update"},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"force": map[string]any{
						"type":        "boolean",
						"description": "Download again even if the checkout looks current, for example after a failed or damaged fetch. Defaults to false.",
					},
				},
				"additionalProperties": false,
			},
		},
	}
	return append(tools, p.fileTools()...), nil
}

// Call runs one source code tool by its local name.
func (p *Provider) Call(ctx context.Context, name string, args map[string]json.RawMessage) (mcptools.Result, error) {
	switch name {
	case "status":
		return p.status(ctx), nil
	case "update":
		return p.update(ctx, boolArg(args, "force")), nil
	case "grep", "read", "find", "ls":
	default:
		return mcptools.Result{}, fmt.Errorf("unknown tool %q", name)
	}

	idx, err := p.ensure(ctx)
	if errors.Is(err, errLoading) {
		return mcptools.Result{IsError: true, Content: fmt.Sprintf(
			"The llama-swap source code (%s) is still downloading. Call this tool again in a few seconds.", p.ref)}, nil
	}
	if errors.Is(err, errNoGit) {
		return mcptools.Result{IsError: true, Content: noGitAdvice + " Answer from the docs__* tools and say the answer was not checked against the code."}, nil
	}
	if err != nil {
		return mcptools.Result{IsError: true, Content: fmt.Sprintf(
			"The llama-swap source code is not available on this server: %v. Call %s to retry, or answer from the "+
				"docs__* tools instead and say the answer was not checked against the code.", err, mcptools.QualifyName(p.ID(), "update"))}, nil
	}

	switch name {
	case "grep":
		return p.grep(idx, args), nil
	case "read":
		return p.read(idx, args), nil
	case "find":
		return p.find(idx, args), nil
	default:
		return p.ls(idx, args), nil
	}
}
