package ollama

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// streamMode selects which Ollama wire shape the writer emits.
type streamMode int

const (
	modeChat streamMode = iota
	modeGenerate
)

// StreamWriter wraps a gin.ResponseWriter, parses an OpenAI SSE chat-completion
// stream as it flows through, and rewrites the body to Ollama-shaped NDJSON
// (one JSON object per line). The wrapper is intended to sit between the
// reverse proxy and the downstream client for the duration of a single request.
//
// If the upstream response is not text/event-stream (e.g. an error reply), the
// writer falls into pass-through mode and forwards bytes unchanged so error
// bodies are preserved verbatim.
type StreamWriter struct {
	http.ResponseWriter
	mode      streamMode
	model     string
	startTime time.Time

	passthrough   bool
	headerWritten bool
	buffer        []byte

	promptTokens int
	evalTokens   int
	doneReason   string
	finalized    bool
}

// NewChatStreamWriter wraps w to emit /api/chat-shaped NDJSON.
func NewChatStreamWriter(w http.ResponseWriter, model string) *StreamWriter {
	return &StreamWriter{
		ResponseWriter: w,
		mode:           modeChat,
		model:          model,
		startTime:      time.Now(),
	}
}

// NewGenerateStreamWriter wraps w to emit /api/generate-shaped NDJSON.
func NewGenerateStreamWriter(w http.ResponseWriter, model string) *StreamWriter {
	return &StreamWriter{
		ResponseWriter: w,
		mode:           modeGenerate,
		model:          model,
		startTime:      time.Now(),
	}
}

// WriteHeader inspects the upstream Content-Type and status. SSE responses are
// translated; anything else is passed through unchanged.
func (w *StreamWriter) WriteHeader(statusCode int) {
	h := w.Header()
	ct := h.Get("Content-Type")
	if statusCode != http.StatusOK || !strings.HasPrefix(ct, "text/event-stream") {
		w.passthrough = true
	} else {
		h.Set("Content-Type", "application/x-ndjson")
		h.Del("Content-Length")
	}
	w.headerWritten = true
	w.ResponseWriter.WriteHeader(statusCode)
}

// Write accumulates SSE bytes, parses complete frames terminated by "\n\n",
// and emits the corresponding NDJSON lines.
func (w *StreamWriter) Write(p []byte) (int, error) {
	if !w.headerWritten {
		w.WriteHeader(http.StatusOK)
	}
	if w.passthrough {
		return w.ResponseWriter.Write(p)
	}

	w.buffer = append(w.buffer, p...)
	for {
		idx := bytes.Index(w.buffer, []byte("\n\n"))
		if idx < 0 {
			break
		}
		frame := w.buffer[:idx]
		w.buffer = w.buffer[idx+2:]
		if err := w.handleFrame(frame); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Finalize emits a final done:true frame if the upstream closed without
// sending an explicit [DONE]. Safe to call repeatedly.
func (w *StreamWriter) Finalize() {
	if !w.headerWritten || w.passthrough || w.finalized {
		return
	}
	_ = w.emitFinal()
}

// openaiStreamChunk is the subset of the SSE delta payload we care about.
type openaiStreamChunk struct {
	Choices []struct {
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int `json:"index"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (w *StreamWriter) handleFrame(frame []byte) error {
	for _, line := range bytes.Split(frame, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if bytes.Equal(payload, []byte("[DONE]")) {
			return w.emitFinal()
		}
		var chunk openaiStreamChunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			// Malformed SSE frame: skip rather than tearing down the stream.
			continue
		}
		if chunk.Usage != nil {
			w.promptTokens = chunk.Usage.PromptTokens
			w.evalTokens = chunk.Usage.CompletionTokens
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			w.doneReason = *choice.FinishReason
		}
		if choice.Delta.Content != "" {
			if err := w.emitDelta(choice.Delta.Content); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *StreamWriter) emitDelta(content string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var payload []byte
	var err error
	switch w.mode {
	case modeChat:
		payload, err = json.Marshal(ChatResponse{
			Model:     w.model,
			CreatedAt: now,
			Message:   Message{Role: "assistant", Content: content},
			Done:      false,
		})
	case modeGenerate:
		payload, err = json.Marshal(GenerateResponse{
			Model:     w.model,
			CreatedAt: now,
			Response:  content,
			Done:      false,
		})
	}
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if _, err := w.ResponseWriter.Write(payload); err != nil {
		return err
	}
	w.flush()
	return nil
}

// flush forwards a flush to the underlying writer when it supports
// http.Flusher, so NDJSON lines reach the client immediately.
func (w *StreamWriter) flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *StreamWriter) emitFinal() error {
	if w.finalized {
		return nil
	}
	w.finalized = true
	if w.doneReason == "" {
		w.doneReason = "stop"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	duration := time.Since(w.startTime).Nanoseconds()
	if duration <= 0 {
		// Guard against a zero reading on coarse-resolution clocks so the
		// total_duration field (omitempty) is always emitted, matching Ollama.
		duration = 1
	}
	var payload []byte
	var err error
	switch w.mode {
	case modeChat:
		payload, err = json.Marshal(ChatResponse{
			Model:           w.model,
			CreatedAt:       now,
			Message:         Message{Role: "assistant", Content: ""},
			Done:            true,
			DoneReason:      w.doneReason,
			TotalDuration:   duration,
			PromptEvalCount: w.promptTokens,
			EvalCount:       w.evalTokens,
		})
	case modeGenerate:
		payload, err = json.Marshal(GenerateResponse{
			Model:           w.model,
			CreatedAt:       now,
			Response:        "",
			Done:            true,
			DoneReason:      w.doneReason,
			TotalDuration:   duration,
			PromptEvalCount: w.promptTokens,
			EvalCount:       w.evalTokens,
		})
	}
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if _, err := w.ResponseWriter.Write(payload); err != nil {
		return err
	}
	w.flush()
	return nil
}
