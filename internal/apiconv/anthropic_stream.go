package apiconv

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// AnthropicStreamWriter wraps an http.ResponseWriter, parses an OpenAI SSE
// chat-completion stream as it flows through, and re-emits it as an Anthropic
// /v1/messages SSE event stream (message_start, content_block_*, message_delta,
// message_stop).
//
// If the upstream response is not text/event-stream (e.g. an error reply) the
// writer falls into pass-through mode and forwards bytes unchanged.
type AnthropicStreamWriter struct {
	http.ResponseWriter
	model string

	passthrough   bool
	headerWritten bool
	buffer        []byte

	// emit state
	messageStarted bool
	finalized      bool
	msgID          string

	openBlockType  string // "", "text", "tool"
	openBlockIndex int
	nextIndex      int
	toolIndexMap   map[int]int // openai tool_call index -> anthropic block index

	promptTokens int
	evalTokens   int
	cachedTokens int
	stopReason   string
}

// NewAnthropicStreamWriter wraps w to emit Anthropic-shaped SSE. model is the
// name the client requested (echoed in message_start).
func NewAnthropicStreamWriter(w http.ResponseWriter, model string) *AnthropicStreamWriter {
	return &AnthropicStreamWriter{
		ResponseWriter: w,
		model:          model,
		toolIndexMap:   map[int]int{},
	}
}

// WriteHeader passes non-SSE replies through unchanged; SSE replies keep the
// text/event-stream content type and are translated.
func (w *AnthropicStreamWriter) WriteHeader(statusCode int) {
	h := w.Header()
	ct := h.Get("Content-Type")
	if statusCode != http.StatusOK || !strings.HasPrefix(ct, "text/event-stream") {
		w.passthrough = true
	} else {
		h.Del("Content-Length")
	}
	w.headerWritten = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *AnthropicStreamWriter) Write(p []byte) (int, error) {
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

// Finalize emits the closing Anthropic events if the upstream closed without an
// explicit [DONE]. Safe to call repeatedly.
func (w *AnthropicStreamWriter) Finalize() {
	if !w.headerWritten || w.passthrough || w.finalized {
		return
	}
	_ = w.emitFinal()
}

// oaStreamChunk is the subset of an OpenAI SSE delta payload we care about.
type oaStreamChunk struct {
	ID      string `json:"id"`
	Choices []struct {
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

func (w *AnthropicStreamWriter) handleFrame(frame []byte) error {
	for _, line := range bytes.Split(frame, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if bytes.Equal(payload, []byte("[DONE]")) {
			return w.emitFinal()
		}
		var chunk oaStreamChunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			// Malformed frame: skip rather than tearing down the stream.
			continue
		}
		if chunk.ID != "" && w.msgID == "" {
			w.msgID = chunk.ID
		}
		if chunk.Usage != nil {
			w.promptTokens = chunk.Usage.PromptTokens
			w.evalTokens = chunk.Usage.CompletionTokens
			w.cachedTokens = chunk.Usage.PromptTokensDetails.CachedTokens
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			w.stopReason = mapFinishReason(*choice.FinishReason)
		}
		if choice.Delta.Content != "" {
			if err := w.handleTextDelta(choice.Delta.Content); err != nil {
				return err
			}
		}
		for _, tc := range choice.Delta.ToolCalls {
			if err := w.handleToolDelta(tc.Index, tc.ID, tc.Function.Name, tc.Function.Arguments); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *AnthropicStreamWriter) handleTextDelta(text string) error {
	if err := w.ensureMessageStarted(); err != nil {
		return err
	}
	if w.openBlockType != "text" {
		if err := w.closeOpenBlock(); err != nil {
			return err
		}
		idx := w.nextIndex
		w.nextIndex++
		if err := w.writeEvent("content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         idx,
			"content_block": map[string]any{"type": "text", "text": ""},
		}); err != nil {
			return err
		}
		w.openBlockType = "text"
		w.openBlockIndex = idx
	}
	return w.writeEvent("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": w.openBlockIndex,
		"delta": map[string]any{"type": "text_delta", "text": text},
	})
}

func (w *AnthropicStreamWriter) handleToolDelta(oaIndex int, id, name, args string) error {
	if err := w.ensureMessageStarted(); err != nil {
		return err
	}
	aIdx, ok := w.toolIndexMap[oaIndex]
	if !ok {
		if err := w.closeOpenBlock(); err != nil {
			return err
		}
		aIdx = w.nextIndex
		w.nextIndex++
		w.toolIndexMap[oaIndex] = aIdx
		if err := w.writeEvent("content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": aIdx,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    id,
				"name":  name,
				"input": map[string]any{},
			},
		}); err != nil {
			return err
		}
		w.openBlockType = "tool"
		w.openBlockIndex = aIdx
	}
	if args != "" {
		return w.writeEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": aIdx,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": args},
		})
	}
	return nil
}

func (w *AnthropicStreamWriter) ensureMessageStarted() error {
	if w.messageStarted {
		return nil
	}
	w.messageStarted = true
	return w.writeEvent("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            anthropicMessageID(w.msgID),
			"type":          "message",
			"role":          "assistant",
			"model":         w.model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})
}

func (w *AnthropicStreamWriter) closeOpenBlock() error {
	if w.openBlockType == "" {
		return nil
	}
	idx := w.openBlockIndex
	w.openBlockType = ""
	return w.writeEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": idx,
	})
}

func (w *AnthropicStreamWriter) emitFinal() error {
	if w.finalized {
		return nil
	}
	w.finalized = true
	if err := w.ensureMessageStarted(); err != nil {
		return err
	}
	if err := w.closeOpenBlock(); err != nil {
		return err
	}
	stop := w.stopReason
	if stop == "" {
		stop = "end_turn"
	}
	usage := map[string]any{"output_tokens": w.evalTokens}
	if w.promptTokens > 0 {
		usage["input_tokens"] = w.promptTokens
	}
	if w.cachedTokens > 0 {
		usage["cache_read_input_tokens"] = w.cachedTokens
	}
	if err := w.writeEvent("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
		"usage": usage,
	}); err != nil {
		return err
	}
	return w.writeEvent("message_stop", map[string]any{"type": "message_stop"})
}

func (w *AnthropicStreamWriter) writeEvent(event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString("event: ")
	buf.WriteString(event)
	buf.WriteByte('\n')
	buf.WriteString("data: ")
	buf.Write(payload)
	buf.WriteString("\n\n")
	if _, err := w.ResponseWriter.Write(buf.Bytes()); err != nil {
		return err
	}
	w.flush()
	return nil
}

// flush forwards a flush to the underlying writer when it supports
// http.Flusher, so SSE events reach the client immediately.
func (w *AnthropicStreamWriter) flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
