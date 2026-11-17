package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tidwall/gjson"
)

var loremWords = strings.Fields(
	"Lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor " +
		"incididunt ut labore et dolore magna aliqua Ut enim ad minim veniam quis nostrud " +
		"exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat Duis aute " +
		"irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla " +
		"pariatur Excepteur sint occaecat cupidatat non proident sunt in culpa qui officia " +
		"deserunt mollit anim id est laborum Sed ut perspiciatis unde omnis iste natus error " +
		"sit voluptatem accusantium doloremque laudantium totam rem aperiam eaque ipsa quae " +
		"ab illo inventore veritatis et quasi architecto beatae vitae dicta sunt explicabo " +
		"Nemo enim ipsam voluptatem quia voluptas sit aspernatur aut odit aut fugit",
)

var (
	flagListen       = flag.String("listen", "localhost:9898", "listen address")
	flagTokens       = flag.Int("tokens", 1000, "number of completion (output) tokens to return")
	flagTPS          = flag.Float64("tps", 75, "completion (output) tokens per second")
	flagPromptTokens = flag.Int("prompt-tokens", 100, "number of prompt (input) tokens to report, cached tokens included")
	flagCacheTokens  = flag.Int("cache-tokens", 0, "how many of the prompt tokens are cached (cache-read); they take no prefill time")
	flagPrefillTPS   = flag.Float64("prefill-tps", 2000, "simulated prompt (prefill) tokens per second")
	flagDraft        = flag.Int("draft-tokens", 0, "speculative decoding: draft tokens (0 omits the Drafted columns)")
	flagDraftAcc     = flag.Int("draft-accepted", 0, "speculative decoding: accepted draft tokens")
	flagLoad         = flag.String("load", "0s", "simulated load duration (e.g. 2s, 500ms)")
)

type chunkDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type chunkChoice struct {
	Index        int        `json:"index"`
	Delta        chunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

// chatChunk is one server-sent event. Usage and Timings are optional and only
// appear on the final chunk; llama-swap reads them to populate its activity
// metrics, and OpenAI clients ignore the extra fields.
type chatChunk struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []chunkChoice  `json:"choices"`
	Usage   map[string]any `json:"usage,omitempty"`
	Timings map[string]any `json:"timings,omitempty"`
}

type completionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completionChoice struct {
	Index        int               `json:"index"`
	Message      completionMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

type chatCompletion struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []completionChoice `json:"choices"`
	Usage   map[string]any     `json:"usage"`
	Timings map[string]any     `json:"timings"`
}

func loremText(n int) string {
	words := make([]string, n)
	for i := range words {
		words[i] = loremWords[i%len(loremWords)]
	}
	return strings.Join(words, " ")
}

// profile is the resolved set of usage and timing values reported on every
// reply. It is computed once in main and shared by both the streaming and
// non-streaming handlers.
type profile struct {
	promptTokens  int
	outputTokens  int
	cacheTokens   int
	prefillTPS    float64
	decodeTPS     float64
	draftTokens   int
	draftAccepted int

	// Derived, consistent values so the reported rates, durations and token
	// counts all agree (as a real backend's would).
	prefillMs    float64
	decodeMs     float64
	promptPerSec float64
	decodePerSec float64
}

// processedTokens is the part of the prompt that is not served from the
// cache. Only these tokens take prefill time.
func (p *profile) processedTokens() int {
	return max(p.promptTokens-p.cacheTokens, 0)
}

func (p *profile) resolve() {
	p.prefillMs, p.promptPerSec = 0, 0
	if n := p.processedTokens(); n > 0 && p.prefillTPS > 0 {
		p.prefillMs = float64(n) / p.prefillTPS * 1000
		p.promptPerSec = p.prefillTPS
	}
	p.decodeMs, p.decodePerSec = 0, 0
	if p.outputTokens > 0 && p.decodeTPS > 0 {
		p.decodeMs = float64(p.outputTokens) / p.decodeTPS * 1000
		p.decodePerSec = p.decodeTPS
	}
}

// usageBlock is the OpenAI-style usage object: prompt/completion/total tokens
// plus the cached read count. It is what the client of the completion reads,
// and it is the fallback llama-swap uses when a timings block is absent.
func (p *profile) usageBlock() map[string]any {
	u := map[string]any{
		"prompt_tokens":     p.promptTokens,
		"completion_tokens": p.outputTokens,
		"total_tokens":      p.promptTokens + p.outputTokens,
	}
	if p.cacheTokens > 0 {
		u["prompt_tokens_details"] = map[string]any{"cached_tokens": p.cacheTokens}
	}
	return u
}

// timingsBlock is a llama-server-style timings object. llama-swap treats it as
// the authoritative source: it overrides prompt/complete counts and supplies the
// prefill and decode rates, the prefill+decode durations, the cached count, and
// the speculative-decoding (drafted / draft-accepted) columns.
func (p *profile) timingsBlock() map[string]any {
	t := map[string]any{
		"prompt_n": p.processedTokens(), // llama-server excludes cache_n

		"predicted_n":          p.outputTokens,
		"cache_n":              p.cacheTokens,
		"prompt_per_second":    p.promptPerSec,
		"predicted_per_second": p.decodePerSec,
		"prompt_ms":            p.prefillMs,
		"predicted_ms":         p.decodeMs,
	}
	if p.draftTokens > 0 {
		t["draft_n"] = p.draftTokens
		t["draft_n_accepted"] = p.draftAccepted
	}
	return t
}

// writeChunk marshals a chunk as a single server-sent event.
func writeChunk(w http.ResponseWriter, chunk chatChunk) error {
	data, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

// newChunk builds a base chunk, reusing the stable id/object/created/model so
// every event in one reply looks like it came from the same completion.
func newChunk(model string) *chatChunk {
	return &chatChunk{
		ID:      "chatcmpl-fake",
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
	}
}

// startLoading runs the countdown log and closes ready when loadDur elapses.
// If loadDur is zero, ready is closed immediately.
func startLoading(loadDur time.Duration) <-chan struct{} {
	ready := make(chan struct{})
	if loadDur == 0 {
		close(ready)
		return ready
	}
	go func() {
		deadline := time.Now().Add(loadDur)
		log.Printf("loading... %s remaining", loadDur.Round(time.Second))
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		timer := time.NewTimer(loadDur)
		for {
			select {
			case <-timer.C:
				close(ready)
				log.Printf("ready")
				return
			case <-ticker.C:
				if rem := time.Until(deadline).Round(time.Second); rem > 0 {
					log.Printf("loading... %s remaining", rem)
				}
			}
		}
	}()
	return ready
}

func healthHandler(ready <-chan struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-ready:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}
}

// sleepFor simulates a real backend stage (prefill or decode) for dur, returning
// early if the client disconnects.
func sleepFor(ctx context.Context, dur time.Duration) bool {
	if dur <= 0 {
		return true
	}
	select {
	case <-time.After(dur):
		return true
	case <-ctx.Done():
		return false
	}
}

func chatHandler(ready <-chan struct{}, prof *profile) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		streaming := gjson.GetBytes(body, "stream").Bool()
		ctx := r.Context()

		select {
		case <-ready:
		case <-ctx.Done():
			return
		}

		// Simulated total time = prefill + decode, so the observed duration lines
		// up with the prompt_ms + predicted_ms the timings block reports.
		if !streaming {
			if !sleepFor(ctx, time.Duration(prof.prefillMs*float64(time.Millisecond))) {
				return
			}
			if !sleepFor(ctx, time.Duration(prof.decodeMs*float64(time.Millisecond))) {
				return
			}
			text := loremText(prof.outputTokens)
			resp := chatCompletion{
				ID:      "chatcmpl-fake",
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   "fake-model",
				Choices: []completionChoice{
					{
						Index:        0,
						Message:      completionMessage{Role: "assistant", Content: text},
						FinishReason: "stop",
					},
				},
				Usage:   prof.usageBlock(),
				Timings: prof.timingsBlock(),
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		// Send role delta first
		first := newChunk("fake-model")
		first.Choices = []chunkChoice{{Index: 0, Delta: chunkDelta{Role: "assistant"}}}
		if err := writeChunk(w, *first); err != nil {
			return
		}
		flusher.Flush()

		// Prefill (time-to-first-token) delay, mirroring a real backend.
		if !sleepFor(ctx, time.Duration(prof.prefillMs*float64(time.Millisecond))) {
			return
		}

		interval := time.Duration(float64(time.Second) / prof.decodeTPS)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		stop := "stop"
		for i := 0; i < prof.outputTokens; i++ {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			word := loremWords[i%len(loremWords)]
			if i < prof.outputTokens-1 {
				word += " "
			}
			chunk := newChunk("fake-model")
			chunk.Choices = []chunkChoice{{Index: 0, Delta: chunkDelta{Content: word}}}
			if err := writeChunk(w, *chunk); err != nil {
				return
			}
			flusher.Flush()
		}

		// Final chunk: an empty delta carrying finish_reason plus the usage and
		// timings blocks, so a buffered stream exposes the same metrics as the
		// non-streaming path. It is sent after the loop (and not attached to the
		// last content chunk) so it is still emitted when -tokens 0 streams no
		// content at all.
		final := newChunk("fake-model")
		final.Choices = []chunkChoice{{Index: 0, FinishReason: &stop}}
		final.Usage = prof.usageBlock()
		final.Timings = prof.timingsBlock()
		if err := writeChunk(w, *final); err != nil {
			return
		}
		flusher.Flush()

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

func main() {
	flag.Parse()

	loadDur, err := time.ParseDuration(*flagLoad)
	if err != nil {
		log.Fatalf("invalid -load value %q: %v", *flagLoad, err)
	}

	prof := &profile{
		promptTokens:  *flagPromptTokens,
		outputTokens:  *flagTokens,
		cacheTokens:   *flagCacheTokens,
		prefillTPS:    *flagPrefillTPS,
		decodeTPS:     *flagTPS,
		draftTokens:   *flagDraft,
		draftAccepted: *flagDraftAcc,
	}
	if prof.decodeTPS <= 0 {
		prof.decodeTPS = 1
	}
	prof.resolve()

	ready := startLoading(loadDur)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler(ready))
	mux.HandleFunc("/v1/chat/completions", chatHandler(ready, prof))

	srv := &http.Server{
		Addr:    *flagListen,
		Handler: mux,
	}

	go func() {
		log.Printf("listening on %s (prompt=%d completion=%d cached=%d prefill=%.0f t/s decode=%.1f t/s draft=%d/%d load=%s)",
			*flagListen, prof.promptTokens, prof.outputTokens, prof.cacheTokens,
			prof.prefillTPS, prof.decodeTPS, prof.draftTokens, prof.draftAccepted, loadDur)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}
