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
	flagListen = flag.String("listen", "localhost:9898", "listen address")
	flagTokens = flag.Int("tokens", 1000, "number of tokens to return")
	flagTPS    = flag.Float64("tps", 75, "tokens per second")
	flagLoad   = flag.String("load", "0s", "simulated load duration (e.g. 2s, 500ms)")
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

type chatChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
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

type completionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatCompletion struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []completionChoice `json:"choices"`
	Usage   completionUsage    `json:"usage"`
}

func loremText(n int) string {
	words := make([]string, n)
	for i := range words {
		words[i] = loremWords[i%len(loremWords)]
	}
	return strings.Join(words, " ")
}

func sendChunk(w http.ResponseWriter, content string, finishReason *string) error {
	chunk := chatChunk{
		ID:      "chatcmpl-fake",
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   "fake-model",
		Choices: []chunkChoice{
			{
				Index:        0,
				Delta:        chunkDelta{Content: content},
				FinishReason: finishReason,
			},
		},
	}
	data, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
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

func chatHandler(ready <-chan struct{}) http.HandlerFunc {
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

		tokens := *flagTokens
		tps := *flagTPS
		if tps <= 0 {
			tps = 1
		}

		if !streaming {
			delay := time.Duration(float64(tokens) / tps * float64(time.Second))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
			text := loremText(tokens)
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
				Usage: completionUsage{
					PromptTokens:     0,
					CompletionTokens: tokens,
					TotalTokens:      tokens,
				},
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
		first := chatChunk{
			ID:      "chatcmpl-fake",
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   "fake-model",
			Choices: []chunkChoice{
				{Index: 0, Delta: chunkDelta{Role: "assistant"}},
			},
		}
		if data, err := json.Marshal(first); err == nil {
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}

		interval := time.Duration(float64(time.Second) / tps)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		stop := "stop"
		for i := 0; i < tokens; i++ {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			word := loremWords[i%len(loremWords)]
			if i < tokens-1 {
				if err := sendChunk(w, word+" ", nil); err != nil {
					return
				}
			} else {
				if err := sendChunk(w, word, &stop); err != nil {
					return
				}
			}
			flusher.Flush()
		}

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

	ready := startLoading(loadDur)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler(ready))
	mux.HandleFunc("/v1/chat/completions", chatHandler(ready))

	srv := &http.Server{
		Addr:    *flagListen,
		Handler: mux,
	}

	go func() {
		log.Printf("listening on %s (tokens=%d tps=%.1f load=%s)",
			*flagListen, *flagTokens, *flagTPS, loadDur)
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
