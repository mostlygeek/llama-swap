package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	xl "github.com/mostlygeek/llama-swap/proxy/translate"
)

// --- StreamParser ---

// StreamParser decodes an OpenAI SSE stream. Each frame is a single line
// starting with `data: ` followed by either `[DONE]` or a chat.completion
// chunk JSON object. Frames are terminated by a blank line.
type StreamParser struct {
	buf      bytes.Buffer
	sawStart bool
	// per-tool-call-index argument buffering is not required for OpenAI→IR
	// (each delta is already a json-string fragment we forward verbatim).
	modelID string
	respID  string
}

func NewStreamParser() xl.StreamParser { return &StreamParser{} }

func (p *StreamParser) Feed(chunk []byte) ([]xl.StreamEvent, error) {
	p.buf.Write(chunk)
	var events []xl.StreamEvent
	for {
		data := p.buf.Bytes()
		// SSE frames are delimited by a blank line. Find the next blank line.
		idx := bytes.Index(data, []byte("\n\n"))
		if idx < 0 {
			// also accept \r\n\r\n
			idx = bytes.Index(data, []byte("\r\n\r\n"))
			if idx < 0 {
				return events, nil
			}
			frame := data[:idx]
			p.buf.Next(idx + 4)
			evs, err := p.parseFrame(frame)
			if err != nil {
				return events, err
			}
			events = append(events, evs...)
			continue
		}
		frame := data[:idx]
		p.buf.Next(idx + 2)
		evs, err := p.parseFrame(frame)
		if err != nil {
			return events, err
		}
		events = append(events, evs...)
	}
}

func (p *StreamParser) Close() ([]xl.StreamEvent, error) {
	// If we never saw [DONE], synthesize a stop event.
	if p.buf.Len() > 0 {
		// best-effort parse remainder
		evs, _ := p.parseFrame(p.buf.Bytes())
		p.buf.Reset()
		return evs, nil
	}
	return nil, nil
}

func (p *StreamParser) parseFrame(frame []byte) ([]xl.StreamEvent, error) {
	frame = bytes.TrimSpace(frame)
	if len(frame) == 0 {
		return nil, nil
	}
	// frame may contain multiple lines; we only care about `data:` lines.
	var dataBuf bytes.Buffer
	for _, line := range bytes.Split(frame, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		if bytes.HasPrefix(line, []byte("data:")) {
			payload := bytes.TrimSpace(line[len("data:"):])
			dataBuf.Write(payload)
		}
	}
	if dataBuf.Len() == 0 {
		return nil, nil
	}
	payload := dataBuf.Bytes()
	if bytes.Equal(payload, []byte("[DONE]")) {
		return []xl.StreamEvent{{Type: xl.StreamStop}}, nil
	}

	var chunk struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Created int64  `json:"created"`
		Choices []struct {
			Index int `json:"index"`
			Delta struct {
				Role      string `json:"role,omitempty"`
				Content   string `json:"content,omitempty"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id,omitempty"`
					Type     string `json:"type,omitempty"`
					Function struct {
						Name      string `json:"name,omitempty"`
						Arguments string `json:"arguments,omitempty"`
					} `json:"function"`
				} `json:"tool_calls,omitempty"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason,omitempty"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage,omitempty"`
	}
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return nil, fmt.Errorf("openai stream: %w", err)
	}

	var events []xl.StreamEvent
	if !p.sawStart {
		p.sawStart = true
		p.modelID = chunk.Model
		p.respID = chunk.ID
		events = append(events, xl.StreamEvent{Type: xl.StreamStart, Model: chunk.Model, ID: chunk.ID})
	}
	for _, ch := range chunk.Choices {
		if ch.Delta.Content != "" {
			events = append(events, xl.StreamEvent{Type: xl.StreamTextDelta, Index: ch.Index, Text: ch.Delta.Content})
		}
		for _, tc := range ch.Delta.ToolCalls {
			if tc.ID != "" || tc.Function.Name != "" {
				events = append(events, xl.StreamEvent{
					Type:  xl.StreamToolUseStart,
					Index: tc.Index,
					ToolCall: &xl.ToolCall{
						ID:   tc.ID,
						Name: tc.Function.Name,
					},
				})
			}
			if tc.Function.Arguments != "" {
				events = append(events, xl.StreamEvent{
					Type:      xl.StreamToolArgsDelta,
					Index:     tc.Index,
					ArgsDelta: tc.Function.Arguments,
				})
			}
		}
		if ch.FinishReason != "" {
			events = append(events, xl.StreamEvent{
				Type:         xl.StreamStop,
				FinishReason: normalizeFinish(ch.FinishReason),
			})
		}
	}
	if chunk.Usage != nil {
		events = append(events, xl.StreamEvent{
			Type: xl.StreamUsage,
			Usage: &xl.Usage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
			},
		})
	}
	return events, nil
}

// --- StreamEmitter ---

// StreamEmitter writes IR events as an OpenAI SSE stream.
type StreamEmitter struct {
	model   string
	id      string
	started bool
	done    bool
	// per-tool-index accumulated state, to emit proper id/name/index on first
	// fragment and args deltas afterwards.
	toolStarted map[int]bool
}

func NewStreamEmitter() xl.StreamEmitter {
	return &StreamEmitter{toolStarted: map[int]bool{}}
}

func (e *StreamEmitter) ContentType() string { return "text/event-stream" }

func (e *StreamEmitter) Emit(w io.Writer, ev xl.StreamEvent) error {
	switch ev.Type {
	case xl.StreamStart:
		if ev.Model != "" {
			e.model = ev.Model
		}
		if ev.ID != "" {
			e.id = ev.ID
		}
		e.started = true
		// OpenAI emits its initial role-only delta as the first chunk.
		return e.writeChunk(w, map[string]any{
			"id":      e.id,
			"object":  "chat.completion.chunk",
			"created": 0,
			"model":   e.model,
			"choices": []map[string]any{{
				"index": 0,
				"delta": map[string]any{"role": "assistant"},
			}},
		})
	case xl.StreamTextDelta:
		return e.writeChunk(w, map[string]any{
			"id":      e.id,
			"object":  "chat.completion.chunk",
			"created": 0,
			"model":   e.model,
			"choices": []map[string]any{{
				"index": 0,
				"delta": map[string]any{"content": ev.Text},
			}},
		})
	case xl.StreamToolUseStart:
		if ev.ToolCall == nil {
			return nil
		}
		e.toolStarted[ev.Index] = true
		tc := map[string]any{
			"index": ev.Index,
			"id":    ev.ToolCall.ID,
			"type":  "function",
			"function": map[string]any{
				"name":      ev.ToolCall.Name,
				"arguments": "",
			},
		}
		return e.writeChunk(w, map[string]any{
			"id": e.id, "object": "chat.completion.chunk", "model": e.model,
			"choices": []map[string]any{{
				"index": 0,
				"delta": map[string]any{"tool_calls": []map[string]any{tc}},
			}},
		})
	case xl.StreamToolArgsDelta:
		tc := map[string]any{
			"index": ev.Index,
			"function": map[string]any{
				"arguments": ev.ArgsDelta,
			},
		}
		return e.writeChunk(w, map[string]any{
			"id": e.id, "object": "chat.completion.chunk", "model": e.model,
			"choices": []map[string]any{{
				"index": 0,
				"delta": map[string]any{"tool_calls": []map[string]any{tc}},
			}},
		})
	case xl.StreamToolUseStop:
		return nil
	case xl.StreamUsage:
		if ev.Usage == nil {
			return nil
		}
		return e.writeChunk(w, map[string]any{
			"id": e.id, "object": "chat.completion.chunk", "model": e.model,
			"choices": []map[string]any{},
			"usage": map[string]any{
				"prompt_tokens":     ev.Usage.PromptTokens,
				"completion_tokens": ev.Usage.CompletionTokens,
				"total_tokens":      ev.Usage.TotalTokens,
			},
		})
	case xl.StreamStop:
		if e.done {
			return nil
		}
		e.done = true
		finish := string(ev.FinishReason)
		if finish == "" {
			finish = "stop"
		}
		if err := e.writeChunk(w, map[string]any{
			"id": e.id, "object": "chat.completion.chunk", "model": e.model,
			"choices": []map[string]any{{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": finish,
			}},
		}); err != nil {
			return err
		}
		_, err := io.WriteString(w, "data: [DONE]\n\n")
		return err
	case xl.StreamError:
		return e.writeChunk(w, map[string]any{
			"error": map[string]any{"message": ev.Err},
		})
	}
	return nil
}

func (e *StreamEmitter) writeChunk(w io.Writer, obj map[string]any) error {
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, "data: "); err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n\n")
	return err
}

// unused import guard
var _ = strings.Repeat
