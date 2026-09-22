// Package capcompat discovers what an upstream inference server can do and
// maps the answer onto llama-swap's model capabilities.
//
// Every supported server exposes /v1/models, and the owned_by field in that
// listing identifies the server family. Detect fetches the listing once, picks
// the matching Prober, and lets it query whatever else it needs. A server
// nobody recognises is not an error worth shouting about: llama-swap also
// fronts image, speech and transcription servers that have no capability
// surface at all.
//
// The data is only readable while the server is running, so callers cache what
// Detect returns. See Service.
package capcompat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// ErrUnsupportedUpstream is returned by Detect when no prober recognises the
// server. It is an expected outcome, not a failure to report loudly.
var ErrUnsupportedUpstream = errors.New("capcompat: unsupported upstream")

// Info is what Detect produces and what callers cache.
type Info struct {
	// Upstream is the server family that answered, e.g. "llama-server".
	Upstream string `json:"upstream"`
	// Capabilities is the discovered subset. Fields a server cannot report
	// are left at their zero value, never guessed.
	Capabilities config.ModelCapConfig `json:"capabilities"`
	// DetectedAt is when the probe ran.
	DetectedAt time.Time `json:"detected_at"`
}

// ModelEntry is one record of an OpenAI-compatible /v1/models listing. It
// carries the union of the fields the supported servers add to the standard
// shape; a server that omits one leaves it at its zero value.
type ModelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`

	// MaxModelLen is vLLM's context length for this entry.
	MaxModelLen int `json:"max_model_len"`
	// Root and Parent are vLLM's adapter lineage. A LoRA adapter has a
	// non-empty Parent naming the base model it is served on top of.
	Root   string `json:"root"`
	Parent string `json:"parent"`

	// Meta is llama-server's model metadata block.
	Meta struct {
		NCtxTrain int `json:"n_ctx_train"`
		NParams   int `json:"n_params"`
	} `json:"meta"`
}

// ModelsResponse is an OpenAI-compatible /v1/models listing.
type ModelsResponse struct {
	Object string       `json:"object"`
	Data   []ModelEntry `json:"data"`
}

// OwnedBy returns the owner reported by the listing. Servers report it per
// entry rather than at the top level, so the first non-empty value wins.
func (m ModelsResponse) OwnedBy() string {
	for _, entry := range m.Data {
		if owner := strings.TrimSpace(entry.OwnedBy); owner != "" {
			return owner
		}
	}
	return ""
}

// Find returns the entry for modelName, or the best stand-in when the name
// does not appear. Servers are free to list a model under a path, an alias or
// a name the client never sees, so an exact miss falls back to the first
// entry that is not an adapter of another model.
func (m ModelsResponse) Find(modelName string) (ModelEntry, bool) {
	name := strings.TrimSpace(modelName)
	if name != "" {
		for _, entry := range m.Data {
			if entry.ID == name {
				return entry, true
			}
		}
	}
	for _, entry := range m.Data {
		if entry.Parent == "" {
			return entry, true
		}
	}
	if len(m.Data) > 0 {
		return m.Data[0], true
	}
	return ModelEntry{}, false
}

// Prober knows one upstream server family: how to recognise it from its model
// listing, and what else to ask it.
type Prober interface {
	// Name identifies the family, and is stored in Info.Upstream.
	Name() string

	// Matches reports whether the listing came from this family.
	Matches(models ModelsResponse) bool

	// Probe maps the server's answers onto capabilities. It may issue more
	// requests through c. Fields the server cannot report are left unset.
	Probe(ctx context.Context, c *Client, models ModelsResponse, modelName string) (config.ModelCapConfig, error)
}

// defaultProbers is the registry Detect consults, in order.
func defaultProbers() []Prober {
	return []Prober{llamaServerProber{}, vllmProber{}}
}

// Detect identifies the upstream behind c and returns what it reports about
// modelName. modelName is the name the upstream knows the model by, which is
// useModelName when set and the llama-swap model ID otherwise; it only matters
// for servers that list more than one model.
//
// It returns ErrUnsupportedUpstream when no prober recognises the server.
func Detect(ctx context.Context, c *Client, modelName string) (Info, error) {
	var models ModelsResponse
	if err := c.GetJSON(ctx, "/v1/models", &models); err != nil {
		return Info{}, fmt.Errorf("capcompat: reading model list: %w", err)
	}

	for _, prober := range defaultProbers() {
		if !prober.Matches(models) {
			continue
		}
		caps, err := prober.Probe(ctx, c, models, modelName)
		if err != nil {
			return Info{}, fmt.Errorf("capcompat: probing %s: %w", prober.Name(), err)
		}
		if err := caps.Validate(); err != nil {
			return Info{}, fmt.Errorf("capcompat: %s reported invalid capabilities: %w", prober.Name(), err)
		}
		return Info{
			Upstream:     prober.Name(),
			Capabilities: caps,
			DetectedAt:   time.Now(),
		}, nil
	}

	owner := models.OwnedBy()
	if owner == "" {
		owner = "unknown"
	}
	return Info{}, fmt.Errorf("%w (owned_by %q)", ErrUnsupportedUpstream, owner)
}

// Client performs GET requests against one upstream server.
type Client struct {
	base       *url.URL
	httpClient *http.Client
	headers    http.Header
}

// maxResponseBytes caps how much of an upstream response is read. /props
// carries a full chat template and is comfortably under this; anything
// larger is not a response this package knows how to use.
const maxResponseBytes = 8 << 20

// NewClient returns a Client for base, honouring the model's proxy timeouts.
// A zero timeout in the config means no timeout, matching the rest of
// llama-swap, but the overall request deadline still comes from the context.
func NewClient(base *url.URL, timeouts config.TimeoutsConfig) *Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		// Same dialer settings the model's own reverse proxy transport uses,
		// so a probe respects timeouts.connect instead of hanging on a dial
		// until the probe deadline.
		DialContext: (&net.Dialer{
			Timeout:   time.Duration(timeouts.Connect) * time.Second,
			KeepAlive: time.Duration(timeouts.KeepAlive) * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   time.Duration(timeouts.TLSHandshake) * time.Second,
		ResponseHeaderTimeout: time.Duration(timeouts.ResponseHeader) * time.Second,
		ExpectContinueTimeout: time.Duration(timeouts.ExpectContinue) * time.Second,
		IdleConnTimeout:       time.Duration(timeouts.IdleConn) * time.Second,
	}
	return &Client{
		base:       base,
		httpClient: &http.Client{Transport: transport},
		headers:    http.Header{},
	}
}

// SetHeader sets a header sent with every request, such as an API key.
func (c *Client) SetHeader(key, value string) {
	if c == nil || value == "" {
		return
	}
	c.headers.Set(key, value)
}

// GetJSON fetches path relative to the client's base URL and decodes the body
// into v. A non-2xx response is an error carrying the status.
func (c *Client) GetJSON(ctx context.Context, path string, v any) error {
	target := c.base.JoinPath(path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return fmt.Errorf("building %s request: %w", path, err)
	}
	req.Header.Set("Accept", "application/json")
	for key, values := range c.headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("requesting %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("requesting %s: upstream returned %s", path, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	return nil
}
