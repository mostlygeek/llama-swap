package translate_test

import (
	"encoding/json"
	"strings"
	"testing"

	xl "github.com/mostlygeek/llama-swap/proxy/translate"
	_ "github.com/mostlygeek/llama-swap/proxy/translate/adapters"
)

func TestChoose(t *testing.T) {
	cases := []struct {
		in     xl.Protocol
		sup    []xl.Protocol
		target xl.Protocol
		needXl bool
	}{
		{xl.ProtocolOpenAI, []xl.Protocol{xl.ProtocolOpenAI, xl.ProtocolAnthropic}, xl.ProtocolOpenAI, false},
		{xl.ProtocolAnthropic, []xl.Protocol{xl.ProtocolOllama}, xl.ProtocolOllama, true},
		{xl.ProtocolOllama, []xl.Protocol{xl.ProtocolAnthropic, xl.ProtocolOpenAI}, xl.ProtocolOpenAI, true},
		{xl.ProtocolAnthropic, []xl.Protocol{xl.ProtocolAnthropic}, xl.ProtocolAnthropic, false},
	}
	for _, c := range cases {
		target, needXl := xl.Choose(c.in, c.sup)
		if target != c.target || needXl != c.needXl {
			t.Errorf("Choose(%v,%v)=(%v,%v) want (%v,%v)", c.in, c.sup, target, needXl, c.target, c.needXl)
		}
	}
}

func TestTranslateRequest_OpenAIToAnthropic(t *testing.T) {
	body := []byte(`{
		"model":"m1",
		"messages":[
			{"role":"system","content":"be brief"},
			{"role":"user","content":"hi"}
		],
		"temperature":0.2,
		"max_tokens":50
	}`)
	out, err := xl.TranslateRequest(xl.ProtocolOpenAI, xl.ProtocolAnthropic, xl.KindChat, body)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["system"] != "be brief" {
		t.Errorf("system: %v", got["system"])
	}
	if got["max_tokens"] == nil {
		t.Errorf("max_tokens missing")
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages: %v", msgs)
	}
}

func TestTranslateResponse_AnthropicToOpenAI(t *testing.T) {
	body := []byte(`{
		"id":"msg_1","type":"message","role":"assistant","model":"m1",
		"content":[{"type":"text","text":"hello"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":3,"output_tokens":1}
	}`)
	out, err := xl.TranslateResponse(xl.ProtocolOpenAI, xl.ProtocolAnthropic, body)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	choices := got["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "hello" {
		t.Errorf("content: %v", msg["content"])
	}
	if !strings.EqualFold(choices[0].(map[string]any)["finish_reason"].(string), "stop") {
		t.Errorf("finish_reason: %v", choices[0])
	}
}

func TestToolIDRoundTrip_OpenAIThroughAnthropic(t *testing.T) {
	// Client is OpenAI. Upstream speaks Anthropic.
	// 1. Client sends assistant tool_call with id=call_abc and then tool role with same id.
	reqBody := []byte(`{
		"model":"m","messages":[
			{"role":"assistant","content":null,"tool_calls":[
				{"id":"call_abc","type":"function","function":{"name":"ping","arguments":"{}"}}
			]},
			{"role":"tool","tool_call_id":"call_abc","content":"pong"}
		]
	}`)
	out, err := xl.TranslateRequest(xl.ProtocolOpenAI, xl.ProtocolAnthropic, xl.KindChat, reqBody)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	// Expect tool_use id to be "toolu_oa_abc" and tool_result id likewise.
	msgs := got["messages"].([]any)
	foundUse, foundRes := false, false
	for _, mi := range msgs {
		m := mi.(map[string]any)
		blocks, _ := m["content"].([]any)
		for _, bi := range blocks {
			b := bi.(map[string]any)
			switch b["type"] {
			case "tool_use":
				if b["id"] == "toolu_oa_abc" {
					foundUse = true
				}
			case "tool_result":
				if b["tool_use_id"] == "toolu_oa_abc" {
					foundRes = true
				}
			}
		}
	}
	if !foundUse || !foundRes {
		t.Fatalf("ID not rewritten in outbound request: %s", out)
	}

	// 2. Upstream (Anthropic) responds with a tool_use id=toolu_oa_abc. The
	// client (OpenAI) should see "call_abc" again.
	respBody := []byte(`{
		"id":"x","type":"message","role":"assistant","model":"m",
		"content":[{"type":"tool_use","id":"toolu_oa_abc","name":"ping","input":{}}],
		"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}
	}`)
	out2, err := xl.TranslateResponse(xl.ProtocolOpenAI, xl.ProtocolAnthropic, respBody)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out2), `"id":"call_abc"`) {
		t.Fatalf("tool id not reversed: %s", out2)
	}
}

func TestTranslateRequest_OpenAIToOllama(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":false,"temperature":0.5}`)
	out, err := xl.TranslateRequest(xl.ProtocolOpenAI, xl.ProtocolOllama, xl.KindChat, body)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["stream"] != false {
		t.Errorf("stream: %v", got["stream"])
	}
	opts := got["options"].(map[string]any)
	if opts["temperature"] != 0.5 {
		t.Errorf("temperature: %v", opts["temperature"])
	}
}
