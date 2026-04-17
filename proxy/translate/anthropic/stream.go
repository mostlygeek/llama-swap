package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	xl "github.com/mostlygeek/llama-swap/proxy/translate"
)

// --- StreamParser ---

// StreamParser decodes an Anthropic SSE stream with typed events.
type StreamParser struct {
	buf bytes.Buffer
	// track per-content-block what kind it is so we can translate deltas.
	blocks map[int]*blockState
}

type blockState struct {
	kind  string // "text" | "tool_use"
	id    string
	name  string
	index int
}

func NewStreamParser() xl.StreamParser {
	return &StreamParser{blocks: map[int]*blockState{}}
}

func (p *StreamParser) Feed(chunk []byte) ([]xl.StreamEvent, error) {
	p.buf.Write(chunk)
	var events []xl.StreamEvent
	for {
		data := p.buf.Bytes()
		idx := bytes.Index(data, []byte("\n\n"))
		if idx < 0 {
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

func (p *StreamParser) Close() ([]xl.StreamEvent, error) { return nil, nil }

func (p *StreamParser) parseFrame(frame []byte) ([]xl.StreamEvent, error) {
	frame = bytes.TrimSpace(frame)
	if len(frame) == 0 {
		return nil, nil
	}
	var eventName string
	var dataBuf bytes.Buffer
	for _, line := range bytes.Split(frame, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		switch {
		case bytes.HasPrefix(line, []byte("event:")):
			eventName = string(bytes.TrimSpace(line[len("event:"):]))
		case bytes.HasPrefix(line, []byte("data:")):
			dataBuf.Write(bytes.TrimSpace(line[len("data:"):]))
		}
	}
	if dataBuf.Len() == 0 {
		return nil, nil
	}
	payload := dataBuf.Bytes()

	var events []xl.StreamEvent
	switch eventName {
	case "message_start":
		var m struct {
			Message struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"message"`
		}
		if err := json.Unmarshal(payload, &m); err != nil {
			return nil, fmt.Errorf("anthropic stream message_start: %w", err)
		}
		events = append(events, xl.StreamEvent{Type: xl.StreamStart, ID: m.Message.ID, Model: m.Message.Model})
	case "content_block_start":
		var b struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal(payload, &b); err != nil {
			return nil, fmt.Errorf("anthropic stream content_block_start: %w", err)
		}
		p.blocks[b.Index] = &blockState{
			kind:  b.ContentBlock.Type,
			id:    b.ContentBlock.ID,
			name:  b.ContentBlock.Name,
			index: b.Index,
		}
		if b.ContentBlock.Type == "tool_use" {
			events = append(events, xl.StreamEvent{
				Type:  xl.StreamToolUseStart,
				Index: b.Index,
				ToolCall: &xl.ToolCall{
					ID:   b.ContentBlock.ID,
					Name: b.ContentBlock.Name,
				},
			})
		} else if b.ContentBlock.Type == "text" && b.ContentBlock.Text != "" {
			events = append(events, xl.StreamEvent{
				Type:  xl.StreamTextDelta,
				Index: b.Index,
				Text:  b.ContentBlock.Text,
			})
		}
	case "content_block_delta":
		var d struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(payload, &d); err != nil {
			return nil, fmt.Errorf("anthropic stream content_block_delta: %w", err)
		}
		switch d.Delta.Type {
		case "text_delta":
			events = append(events, xl.StreamEvent{Type: xl.StreamTextDelta, Index: d.Index, Text: d.Delta.Text})
		case "input_json_delta":
			events = append(events, xl.StreamEvent{Type: xl.StreamToolArgsDelta, Index: d.Index, ArgsDelta: d.Delta.PartialJSON})
		}
	case "content_block_stop":
		var b struct {
			Index int `json:"index"`
		}
		_ = json.Unmarshal(payload, &b)
		if st, ok := p.blocks[b.Index]; ok && st.kind == "tool_use" {
			events = append(events, xl.StreamEvent{Type: xl.StreamToolUseStop, Index: b.Index})
		}
	case "message_delta":
		var m struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage *struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage,omitempty"`
		}
		if err := json.Unmarshal(payload, &m); err != nil {
			return nil, fmt.Errorf("anthropic stream message_delta: %w", err)
		}
		if m.Usage != nil {
			events = append(events, xl.StreamEvent{
				Type:  xl.StreamUsage,
				Usage: &xl.Usage{CompletionTokens: m.Usage.OutputTokens, TotalTokens: m.Usage.OutputTokens},
			})
		}
		events = append(events, xl.StreamEvent{
			Type:         xl.StreamStop,
			FinishReason: normStop(m.Delta.StopReason),
		})
	case "message_stop":
		// final terminator; StreamStop already emitted on message_delta
	case "error":
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(payload, &e)
		events = append(events, xl.StreamEvent{Type: xl.StreamError, Err: e.Error.Message})
	case "ping":
		// ignore
	}
	return events, nil
}

// --- StreamEmitter ---

type StreamEmitter struct {
	id      string
	model   string
	started bool
	// per-tool index: tracks whether we've already emitted content_block_start
	toolStarted map[int]bool
	// which content block index is "open" as a text block (0 is conventional)
	textOpen bool
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
		return writeSSE(w, "message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            e.id,
				"type":          "message",
				"role":          "assistant",
				"model":         e.model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
			},
		})
	case xl.StreamTextDelta:
		if !e.textOpen {
			e.textOpen = true
			if err := writeSSE(w, "content_block_start", map[string]any{
				"type":          "content_block_start",
				"index":         0,
				"content_block": map[string]any{"type": "text", "text": ""},
			}); err != nil {
				return err
			}
		}
		return writeSSE(w, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{"type": "text_delta", "text": ev.Text},
		})
	case xl.StreamToolUseStart:
		if ev.ToolCall == nil {
			return nil
		}
		idx := ev.Index + 1 // shift so text block can be index 0
		if e.textOpen {
			// close text block first
		}
		e.toolStarted[idx] = true
		return writeSSE(w, "content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": idx,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    ev.ToolCall.ID,
				"name":  ev.ToolCall.Name,
				"input": map[string]any{},
			},
		})
	case xl.StreamToolArgsDelta:
		idx := ev.Index + 1
		return writeSSE(w, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": idx,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": ev.ArgsDelta},
		})
	case xl.StreamToolUseStop:
		idx := ev.Index + 1
		return writeSSE(w, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": idx,
		})
	case xl.StreamUsage:
		return nil
	case xl.StreamStop:
		if e.textOpen {
			if err := writeSSE(w, "content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": 0,
			}); err != nil {
				return err
			}
			e.textOpen = false
		}
		stop := "end_turn"
		switch ev.FinishReason {
		case xl.FinishLength:
			stop = "max_tokens"
		case xl.FinishToolCalls:
			stop = "tool_use"
		}
		if err := writeSSE(w, "message_delta", map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
			"usage": map[string]any{"output_tokens": 0},
		}); err != nil {
			return err
		}
		return writeSSE(w, "message_stop", map[string]any{"type": "message_stop"})
	case xl.StreamError:
		return writeSSE(w, "error", map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "api_error", "message": ev.Err},
		})
	}
	return nil
}

func writeSSE(w io.Writer, event string, obj any) error {
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: ", event); err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n\n")
	return err
}
