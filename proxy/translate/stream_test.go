package translate_test

import (
	"bytes"
	"strings"
	"testing"

	xl "github.com/mostlygeek/llama-swap/proxy/translate"
	_ "github.com/mostlygeek/llama-swap/proxy/translate/adapters"
)

// translateStream is a tiny helper that runs chunks through a parser of
// upstream then an emitter of client and returns the emitted bytes.
func translateStream(t *testing.T, upstream, client xl.Protocol, chunks [][]byte) string {
	t.Helper()
	sp, err := xl.NewStreamParser(upstream)
	if err != nil {
		t.Fatal(err)
	}
	se, err := xl.NewStreamEmitter(client)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	for _, c := range chunks {
		evs, err := sp.Feed(c)
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range evs {
			if err := se.Emit(&buf, ev); err != nil {
				t.Fatal(err)
			}
		}
	}
	final, _ := sp.Close()
	for _, ev := range final {
		_ = se.Emit(&buf, ev)
	}
	return buf.String()
}

func TestStream_OpenAIToAnthropic(t *testing.T) {
	upstream := [][]byte{
		[]byte(`data: {"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n"),
		[]byte(`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"hi "}}]}` + "\n\n"),
		[]byte(`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"there"}}]}` + "\n\n"),
		[]byte(`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"),
		[]byte("data: [DONE]\n\n"),
	}
	out := translateStream(t, xl.ProtocolOpenAI, xl.ProtocolAnthropic, upstream)
	for _, want := range []string{
		"event: message_start",
		"event: content_block_start",
		`"text":"hi "`,
		`"text":"there"`,
		"event: content_block_stop",
		"event: message_delta",
		"event: message_stop",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestStream_AnthropicToOpenAI(t *testing.T) {
	upstream := [][]byte{
		[]byte("event: message_start\ndata: " + `{"type":"message_start","message":{"id":"msg_1","model":"m"}}` + "\n\n"),
		[]byte("event: content_block_start\ndata: " + `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n"),
		[]byte("event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hel"}}` + "\n\n"),
		[]byte("event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}` + "\n\n"),
		[]byte("event: content_block_stop\ndata: " + `{"type":"content_block_stop","index":0}` + "\n\n"),
		[]byte("event: message_delta\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}` + "\n\n"),
		[]byte("event: message_stop\ndata: " + `{"type":"message_stop"}` + "\n\n"),
	}
	out := translateStream(t, xl.ProtocolAnthropic, xl.ProtocolOpenAI, upstream)
	for _, want := range []string{
		`"delta":{"role":"assistant"}`,
		`"content":"hel"`,
		`"content":"lo"`,
		`"finish_reason":"stop"`,
		"data: [DONE]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestStream_OllamaToOpenAI(t *testing.T) {
	upstream := [][]byte{
		[]byte(`{"model":"m","message":{"role":"assistant","content":"foo"},"done":false}` + "\n"),
		[]byte(`{"model":"m","message":{"role":"assistant","content":"bar"},"done":false}` + "\n"),
		[]byte(`{"model":"m","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":3,"eval_count":2}` + "\n"),
	}
	out := translateStream(t, xl.ProtocolOllama, xl.ProtocolOpenAI, upstream)
	for _, want := range []string{
		`"content":"foo"`,
		`"content":"bar"`,
		`"finish_reason":"stop"`,
		"data: [DONE]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestStream_ChunkedFrames(t *testing.T) {
	// Split the same OpenAI chunk across two writes — parser must buffer.
	chunks := [][]byte{
		[]byte("data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":"),
		[]byte("{\"content\":\"ok\"}}]}\n\n"),
		[]byte("data: [DONE]\n\n"),
	}
	out := translateStream(t, xl.ProtocolOpenAI, xl.ProtocolAnthropic, chunks)
	if !strings.Contains(out, `"text":"ok"`) {
		t.Errorf("chunked frame lost: %s", out)
	}
}
