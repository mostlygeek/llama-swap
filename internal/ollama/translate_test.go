package ollama

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// unmarshal helper used in assertions; t.Fatalf on failure.
func mustUnmarshal(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, b)
	}
	return m
}

func boolPtr(b bool) *bool { return &b }

func TestTranslateChatRequest_BasicNonStream(t *testing.T) {
	req := &ChatRequest{
		Model: "qwen3:8b",
		Messages: []Message{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hi"},
		},
	}
	out, err := TranslateChatRequest(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got := mustUnmarshal(t, out)
	if got["model"] != "qwen3:8b" {
		t.Errorf("model: got %v", got["model"])
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages: got %d, want 2", len(msgs))
	}
	if _, ok := got["stream"]; ok {
		t.Errorf("stream should be absent when nil pointer; got %v", got["stream"])
	}
	if _, ok := got["stream_options"]; ok {
		t.Errorf("stream_options should be absent when not streaming")
	}
}

func TestTranslateChatRequest_StreamingAddsIncludeUsage(t *testing.T) {
	req := &ChatRequest{
		Model:    "qwen3:8b",
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Stream:   boolPtr(true),
	}
	out, err := TranslateChatRequest(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got := mustUnmarshal(t, out)
	if got["stream"] != true {
		t.Errorf("stream: got %v", got["stream"])
	}
	so, ok := got["stream_options"].(map[string]any)
	if !ok {
		t.Fatalf("stream_options missing or wrong type: %T", got["stream_options"])
	}
	if so["include_usage"] != true {
		t.Errorf("include_usage: got %v", so["include_usage"])
	}
}

func TestTranslateChatRequest_OptionsMapping(t *testing.T) {
	req := &ChatRequest{
		Model:    "qwen3:8b",
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Options: map[string]any{
			"temperature":    0.7,
			"top_p":          0.9,
			"num_predict":    256,
			"stop":           []string{"\n\n"},
			"seed":           42,
			"unknown_option": "drop me",
		},
	}
	out, err := TranslateChatRequest(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got := mustUnmarshal(t, out)
	if got["temperature"] != 0.7 {
		t.Errorf("temperature: got %v", got["temperature"])
	}
	if got["top_p"] != 0.9 {
		t.Errorf("top_p: got %v", got["top_p"])
	}
	// num_predict -> max_tokens (renamed)
	if got["max_tokens"] != float64(256) {
		t.Errorf("max_tokens: got %v", got["max_tokens"])
	}
	if got["seed"] != float64(42) {
		t.Errorf("seed: got %v", got["seed"])
	}
	if _, ok := got["unknown_option"]; ok {
		t.Errorf("unknown_option must not be forwarded")
	}
	if _, ok := got["num_predict"]; ok {
		t.Errorf("num_predict should be renamed to max_tokens, not passed through")
	}
}

func TestTranslateChatRequest_FormatJSONString(t *testing.T) {
	req := &ChatRequest{
		Model:    "qwen3:8b",
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Format:   json.RawMessage(`"json"`),
	}
	out, err := TranslateChatRequest(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got := mustUnmarshal(t, out)
	rf, ok := got["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing: %v", got)
	}
	if rf["type"] != "json_object" {
		t.Errorf("response_format type: got %v", rf["type"])
	}
}

func TestTranslateChatRequest_FormatSchemaObject(t *testing.T) {
	req := &ChatRequest{
		Model:    "qwen3:8b",
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Format:   json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`),
	}
	out, err := TranslateChatRequest(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got := mustUnmarshal(t, out)
	rf, ok := got["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing: %v", got)
	}
	if rf["type"] != "json_schema" {
		t.Errorf("response_format type: got %v", rf["type"])
	}
	js, ok := rf["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("json_schema missing: %v", rf)
	}
	if _, ok := js["schema"]; !ok {
		t.Errorf("json_schema.schema missing")
	}
}

func TestTranslateChatRequest_KeepAliveDropped(t *testing.T) {
	req := &ChatRequest{
		Model:     "qwen3:8b",
		Messages:  []Message{{Role: "user", Content: "Hi"}},
		KeepAlive: json.RawMessage(`"5m"`),
	}
	out, err := TranslateChatRequest(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got := mustUnmarshal(t, out)
	if _, ok := got["keep_alive"]; ok {
		t.Errorf("keep_alive must be dropped (llama-swap TTL handles this)")
	}
}

func TestTranslateChatRequest_ImagesBecomeContentParts(t *testing.T) {
	req := &ChatRequest{
		Model: "qwen3:8b",
		Messages: []Message{
			{Role: "user", Content: "What's in this?", Images: []string{"YWJj"}},
		},
	}
	out, err := TranslateChatRequest(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got := mustUnmarshal(t, out)
	msgs := got["messages"].([]any)
	user := msgs[0].(map[string]any)
	parts, ok := user["content"].([]any)
	if !ok {
		t.Fatalf("user content should be array of parts when images present, got %T", user["content"])
	}
	if len(parts) != 2 {
		t.Fatalf("parts: want 2, got %d", len(parts))
	}
	imgPart := parts[1].(map[string]any)
	if imgPart["type"] != "image_url" {
		t.Errorf("part[1].type: got %v", imgPart["type"])
	}
	url := imgPart["image_url"].(map[string]any)["url"].(string)
	if !strings.HasPrefix(url, "data:image/jpeg;base64,") {
		t.Errorf("image_url.url: bad prefix: %s", url)
	}
}

func TestTranslateGenerateRequest_WithSystem(t *testing.T) {
	req := &GenerateRequest{
		Model:  "qwen3:8b",
		Prompt: "hi",
		System: "you are pirate",
	}
	out, err := TranslateGenerateRequest(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got := mustUnmarshal(t, out)
	msgs := got["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages: want 2, got %d", len(msgs))
	}
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Errorf("first msg role: got %v", msgs[0].(map[string]any)["role"])
	}
	if msgs[1].(map[string]any)["content"] != "hi" {
		t.Errorf("second msg content: got %v", msgs[1].(map[string]any)["content"])
	}
}

func TestTranslateGenerateRequest_RawSkipsSystem(t *testing.T) {
	req := &GenerateRequest{
		Model:  "qwen3:8b",
		Prompt: "<|im_start|>raw text",
		System: "ignored",
		Raw:    true,
	}
	out, err := TranslateGenerateRequest(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got := mustUnmarshal(t, out)
	msgs := got["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("raw mode: want 1 message, got %d", len(msgs))
	}
	if msgs[0].(map[string]any)["role"] != "user" {
		t.Errorf("raw mode role: got %v", msgs[0].(map[string]any)["role"])
	}
}

func TestTranslateEmbedRequest_StringInput(t *testing.T) {
	req := &EmbedRequest{
		Model: "embed-model",
		Input: json.RawMessage(`"hello"`),
	}
	out, err := TranslateEmbedRequest(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got := mustUnmarshal(t, out)
	if got["input"] != "hello" {
		t.Errorf("input: got %v", got["input"])
	}
}

func TestTranslateEmbedRequest_ArrayInput(t *testing.T) {
	req := &EmbedRequest{
		Model: "embed-model",
		Input: json.RawMessage(`["a","b"]`),
	}
	out, err := TranslateEmbedRequest(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got := mustUnmarshal(t, out)
	arr, ok := got["input"].([]any)
	if !ok {
		t.Fatalf("input should be array, got %T", got["input"])
	}
	if len(arr) != 2 {
		t.Errorf("input length: got %d", len(arr))
	}
}

func TestTranslateEmbeddingsRequest_PromptToInput(t *testing.T) {
	req := &EmbeddingsRequest{
		Model:  "embed-model",
		Prompt: "hello",
	}
	out, err := TranslateEmbeddingsRequest(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got := mustUnmarshal(t, out)
	if got["input"] != "hello" {
		t.Errorf("input: got %v", got["input"])
	}
	if _, ok := got["prompt"]; ok {
		t.Errorf("prompt must not be forwarded")
	}
}

func TestTranslateChatResponse_BasicShape(t *testing.T) {
	openaiBody := []byte(`{
		"id": "chatcmpl-1",
		"choices": [{
			"message": {"role": "assistant", "content": "hello"},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 5, "completion_tokens": 7}
	}`)
	out, err := TranslateChatResponse(openaiBody, "qwen3:8b", 123*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var resp ChatResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("invalid output JSON: %v", err)
	}
	if resp.Model != "qwen3:8b" {
		t.Errorf("model: got %s", resp.Model)
	}
	if !resp.Done {
		t.Errorf("done: got false, want true")
	}
	if resp.DoneReason != "stop" {
		t.Errorf("done_reason: got %s", resp.DoneReason)
	}
	if resp.Message.Content != "hello" {
		t.Errorf("content: got %s", resp.Message.Content)
	}
	if resp.PromptEvalCount != 5 || resp.EvalCount != 7 {
		t.Errorf("token counts: prompt=%d eval=%d", resp.PromptEvalCount, resp.EvalCount)
	}
	if resp.TotalDuration <= 0 {
		t.Errorf("total_duration should be >0, got %d", resp.TotalDuration)
	}
}

func TestTranslateChatResponse_WithToolCalls(t *testing.T) {
	openaiBody := []byte(`{
		"choices":[{
			"message":{
				"role":"assistant",
				"content":"",
				"tool_calls":[{"function":{"name":"get_weather","arguments":"{\"city\":\"NYC\"}"}}]
			},
			"finish_reason":"tool_calls"
		}],
		"usage":{"prompt_tokens":3,"completion_tokens":2}
	}`)
	out, err := TranslateChatResponse(openaiBody, "m", time.Second)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var resp ChatResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("invalid output JSON: %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls: got %d, want 1", len(resp.Message.ToolCalls))
	}
	if resp.Message.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("tool name: got %s", resp.Message.ToolCalls[0].Function.Name)
	}
	if resp.Message.ToolCalls[0].Function.Arguments["city"] != "NYC" {
		t.Errorf("tool args: got %v", resp.Message.ToolCalls[0].Function.Arguments)
	}
	if resp.DoneReason != "tool_calls" {
		t.Errorf("done_reason: got %s", resp.DoneReason)
	}
}

func TestTranslateGenerateResponse_BasicShape(t *testing.T) {
	openaiBody := []byte(`{
		"choices":[{"message":{"role":"assistant","content":"42"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":1,"completion_tokens":1}
	}`)
	out, err := TranslateGenerateResponse(openaiBody, "m", time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var resp GenerateResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("invalid output JSON: %v", err)
	}
	if resp.Response != "42" {
		t.Errorf("response: got %s", resp.Response)
	}
	if !resp.Done {
		t.Errorf("done: got false")
	}
}

func TestTranslateEmbedResponse_ArrayOfArrays(t *testing.T) {
	openaiBody := []byte(`{
		"data":[{"embedding":[1.0,2.0]},{"embedding":[3.0,4.0]}],
		"usage":{"prompt_tokens":2}
	}`)
	out, err := TranslateEmbedResponse(openaiBody, "m", time.Second)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var resp EmbedResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("invalid output JSON: %v", err)
	}
	if len(resp.Embeddings) != 2 {
		t.Fatalf("embeddings count: got %d", len(resp.Embeddings))
	}
	if resp.Embeddings[0][0] != 1.0 || resp.Embeddings[1][1] != 4.0 {
		t.Errorf("values incorrect: %v", resp.Embeddings)
	}
	if resp.PromptEvalCount != 2 {
		t.Errorf("prompt_eval_count: got %d", resp.PromptEvalCount)
	}
}

func TestTranslateEmbeddingsResponse_SingleVector(t *testing.T) {
	openaiBody := []byte(`{"data":[{"embedding":[0.1,0.2,0.3]}]}`)
	out, err := TranslateEmbeddingsResponse(openaiBody)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var resp EmbeddingsResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("invalid output JSON: %v", err)
	}
	if len(resp.Embedding) != 3 {
		t.Errorf("embedding length: got %d", len(resp.Embedding))
	}
}

func TestTranslateEmbeddingsResponse_Empty(t *testing.T) {
	openaiBody := []byte(`{"data":[]}`)
	out, err := TranslateEmbeddingsResponse(openaiBody)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(string(out), `"embedding":[]`) {
		t.Errorf("expected empty embedding array, got %s", out)
	}
}
