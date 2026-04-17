package ollama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	xl "github.com/mostlygeek/llama-swap/proxy/translate"
)

// --- StreamParser ---

// StreamParser decodes an Ollama NDJSON stream. Each frame is a single
// newline-terminated JSON object; a frame with `"done": true` is terminal.
type StreamParser struct {
	buf         bytes.Buffer
	started     bool
	toolNextIdx int
}

func NewStreamParser() xl.StreamParser { return &StreamParser{} }

func (p *StreamParser) Feed(chunk []byte) ([]xl.StreamEvent, error) {
	p.buf.Write(chunk)
	var events []xl.StreamEvent
	for {
		data := p.buf.Bytes()
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			return events, nil
		}
		line := data[:idx]
		p.buf.Next(idx + 1)
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		evs, err := p.parseLine(line)
		if err != nil {
			return events, err
		}
		events = append(events, evs...)
	}
}

func (p *StreamParser) Close() ([]xl.StreamEvent, error) {
	if p.buf.Len() == 0 {
		return nil, nil
	}
	line := bytes.TrimSpace(p.buf.Bytes())
	p.buf.Reset()
	if len(line) == 0 {
		return nil, nil
	}
	return p.parseLine(line)
}

func (p *StreamParser) parseLine(line []byte) ([]xl.StreamEvent, error) {
	var frame struct {
		Model   string `json:"model"`
		Message struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls,omitempty"`
		} `json:"message"`
		Done            bool   `json:"done"`
		DoneReason      string `json:"done_reason"`
		Response        string `json:"response"` // /api/generate
		PromptEvalCount int    `json:"prompt_eval_count"`
		EvalCount       int    `json:"eval_count"`
	}
	if err := json.Unmarshal(line, &frame); err != nil {
		return nil, fmt.Errorf("ollama stream: %w", err)
	}
	var events []xl.StreamEvent
	if !p.started {
		p.started = true
		events = append(events, xl.StreamEvent{Type: xl.StreamStart, Model: frame.Model})
	}
	if frame.Message.Content != "" {
		events = append(events, xl.StreamEvent{Type: xl.StreamTextDelta, Text: frame.Message.Content})
	}
	if frame.Response != "" {
		events = append(events, xl.StreamEvent{Type: xl.StreamTextDelta, Text: frame.Response})
	}
	for _, tc := range frame.Message.ToolCalls {
		id := fmt.Sprintf("call_ol_%s_%d", tc.Function.Name, p.toolNextIdx)
		idx := p.toolNextIdx
		p.toolNextIdx++
		events = append(events, xl.StreamEvent{
			Type:     xl.StreamToolUseStart,
			Index:    idx,
			ToolCall: &xl.ToolCall{ID: id, Name: tc.Function.Name, Arguments: tc.Function.Arguments},
		})
		if len(tc.Function.Arguments) > 0 {
			events = append(events, xl.StreamEvent{
				Type:      xl.StreamToolArgsDelta,
				Index:     idx,
				ArgsDelta: string(tc.Function.Arguments),
			})
		}
		events = append(events, xl.StreamEvent{Type: xl.StreamToolUseStop, Index: idx})
	}
	if frame.Done {
		if frame.PromptEvalCount > 0 || frame.EvalCount > 0 {
			events = append(events, xl.StreamEvent{
				Type: xl.StreamUsage,
				Usage: &xl.Usage{
					PromptTokens:     frame.PromptEvalCount,
					CompletionTokens: frame.EvalCount,
					TotalTokens:      frame.PromptEvalCount + frame.EvalCount,
				},
			})
		}
		finish := xl.FinishStop
		switch frame.DoneReason {
		case "length":
			finish = xl.FinishLength
		case "tool_calls":
			finish = xl.FinishToolCalls
		}
		events = append(events, xl.StreamEvent{Type: xl.StreamStop, FinishReason: finish})
	}
	return events, nil
}

// --- StreamEmitter ---

type StreamEmitter struct {
	model   string
	started bool
	// buffer per-tool arguments until StreamToolUseStop; Ollama emits full
	// arguments JSON per frame and we can't emit partials.
	toolName map[int]string
	toolArgs map[int]*bytes.Buffer
}

func NewStreamEmitter() xl.StreamEmitter {
	return &StreamEmitter{
		toolName: map[int]string{},
		toolArgs: map[int]*bytes.Buffer{},
	}
}

func (e *StreamEmitter) ContentType() string { return "application/x-ndjson" }

func (e *StreamEmitter) Emit(w io.Writer, ev xl.StreamEvent) error {
	switch ev.Type {
	case xl.StreamStart:
		if ev.Model != "" {
			e.model = ev.Model
		}
		e.started = true
		return nil
	case xl.StreamTextDelta:
		return writeNDJSON(w, map[string]any{
			"model":      e.model,
			"created_at": nowRFC3339(),
			"message":    map[string]any{"role": "assistant", "content": ev.Text},
			"done":       false,
		})
	case xl.StreamToolUseStart:
		if ev.ToolCall != nil {
			e.toolName[ev.Index] = ev.ToolCall.Name
			e.toolArgs[ev.Index] = &bytes.Buffer{}
			if len(ev.ToolCall.Arguments) > 0 {
				e.toolArgs[ev.Index].Write(ev.ToolCall.Arguments)
			}
		}
		return nil
	case xl.StreamToolArgsDelta:
		if buf, ok := e.toolArgs[ev.Index]; ok {
			buf.WriteString(ev.ArgsDelta)
		}
		return nil
	case xl.StreamToolUseStop:
		name := e.toolName[ev.Index]
		args := json.RawMessage(`{}`)
		if buf, ok := e.toolArgs[ev.Index]; ok && buf.Len() > 0 {
			args = buf.Bytes()
		}
		delete(e.toolName, ev.Index)
		delete(e.toolArgs, ev.Index)
		return writeNDJSON(w, map[string]any{
			"model":      e.model,
			"created_at": nowRFC3339(),
			"message": map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []map[string]any{{
					"function": map[string]any{
						"name":      name,
						"arguments": args,
					},
				}},
			},
			"done": false,
		})
	case xl.StreamUsage:
		return nil // folded into final done frame
	case xl.StreamStop:
		done := map[string]any{
			"model":      e.model,
			"created_at": nowRFC3339(),
			"message":    map[string]any{"role": "assistant", "content": ""},
			"done":       true,
		}
		switch ev.FinishReason {
		case xl.FinishLength:
			done["done_reason"] = "length"
		case xl.FinishToolCalls:
			done["done_reason"] = "tool_calls"
		default:
			done["done_reason"] = "stop"
		}
		return writeNDJSON(w, done)
	case xl.StreamError:
		return writeNDJSON(w, map[string]any{"error": ev.Err})
	}
	return nil
}

func writeNDJSON(w io.Writer, obj any) error {
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n")
	return err
}

func nowRFC3339() string {
	return timeNow()
}
