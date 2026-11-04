package server

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestServer_Realtime_WebsocketUpgradeTunnelsToModel checks that
// GET /v1/realtime?model=... is dispatched by its query model and that the
// upgraded connection is tunnelled both ways through the full model
// middleware chain, whose response writer wrappers must allow hijacking.
func TestServer_Realtime_WebsocketUpgradeTunnelsToModel(t *testing.T) {
	gotPath := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath <- r.URL.Path + "?" + r.URL.RawQuery
		conn, brw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("upstream hijack: %v", err)
			return
		}
		defer conn.Close()
		fmt.Fprint(brw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		brw.Flush()
		// echo one line back, uppercased
		line, err := brw.ReadString('\n')
		if err != nil {
			return
		}
		brw.WriteString(strings.ToUpper(line))
		brw.Flush()
	}))
	defer upstream.Close()

	target, _ := url.Parse(upstream.URL)
	local := newStubRouter([]string{"rt-model"}, "")
	local.serveHTTP = httputil.NewSingleHostReverseProxy(target).ServeHTTP

	front := httptest.NewServer(newTestServer(local, newStubRouter(nil, "")))
	defer front.Close()

	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(front.URL, "http://"), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	fmt.Fprint(conn, "GET /v1/realtime?model=rt-model HTTP/1.1\r\n"+
		"Host: test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n"+
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 101; body %q", resp.StatusCode, body)
	}
	if p := <-gotPath; p != "/v1/realtime?model=rt-model" {
		t.Errorf("upstream request = %q, want /v1/realtime?model=rt-model", p)
	}

	fmt.Fprint(conn, "session.update\n")
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line != "SESSION.UPDATE\n" {
		t.Errorf("tunnelled reply = %q, want SESSION.UPDATE", line)
	}
}

func TestServer_Realtime_RequiresModel(t *testing.T) {
	s := newTestServer(newStubRouter([]string{"rt-model"}, "ok"), newStubRouter(nil, ""))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/realtime", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when no model is named", w.Code)
	}
}

// wsFrame builds one websocket frame. Client frames are masked, as browsers
// must send them.
func wsFrame(op byte, fin, masked bool, payload []byte) []byte {
	b0 := op
	if fin {
		b0 |= 0x80
	}
	frame := []byte{b0}
	var maskBit byte
	if masked {
		maskBit = 0x80
	}
	switch n := len(payload); {
	case n < 126:
		frame = append(frame, maskBit|byte(n))
	case n <= 0xffff:
		frame = append(frame, maskBit|126, byte(n>>8), byte(n))
	default:
		frame = append(frame, maskBit|127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(n))
	}
	if !masked {
		return append(frame, payload...)
	}
	key := []byte{0x12, 0x34, 0x56, 0x78}
	frame = append(frame, key...)
	for i, c := range payload {
		frame = append(frame, c^key[i%4])
	}
	return frame
}

func textFrame(masked bool, s string) []byte { return wsFrame(0x1, true, masked, []byte(s)) }

func TestServer_Realtime_WsReaderReassemblesMessages(t *testing.T) {
	var got []string
	r := wsReader{onText: func(b []byte) { got = append(got, string(b)) }}

	var stream []byte
	stream = append(stream, textFrame(true, `{"a":1}`)...)
	// a fragmented message with a ping between its fragments
	stream = append(stream, wsFrame(0x1, false, true, []byte(`{"b":`))...)
	stream = append(stream, wsFrame(0x9, true, true, []byte("ping"))...)
	stream = append(stream, wsFrame(0x0, true, true, []byte(`2}`))...)
	// a 16-bit length frame, and a binary frame that is not reported
	stream = append(stream, textFrame(false, `{"c":"`+strings.Repeat("x", 300)+`"}`)...)
	stream = append(stream, wsFrame(0x2, true, false, []byte{1, 2, 3})...)

	// one byte at a time, as a slow connection might deliver it
	for i := range stream {
		r.feed(stream[i : i+1])
	}
	if len(got) != 3 || got[0] != `{"a":1}` || got[1] != `{"b":2}` || len(got[2]) != 308 {
		t.Fatalf("messages = %q", got)
	}
}

func TestServer_Realtime_WsReaderSkipsOversizedMessages(t *testing.T) {
	var got []string
	r := wsReader{onText: func(b []byte) { got = append(got, string(b)) }}

	big := wsFrame(0x1, true, false, make([]byte, maxRealtimeMessage+1))
	stream := append(append(big, textFrame(false, `{"after":true}`)...), textFrame(false, `{"x":1}`)...)
	for len(stream) > 0 {
		n := min(len(stream), 64<<10)
		r.feed(stream[:n])
		stream = stream[n:]
	}
	if len(got) != 2 || got[0] != `{"after":true}` {
		t.Fatalf("messages = %q, want only the two small ones", got)
	}
}

func TestServer_Realtime_SessionTallies(t *testing.T) {
	s := newRealtimeSession()
	clock := time.Unix(0, 0)
	s.now = func() time.Time { return clock }

	// 0.5s of 16 kHz PCM16 is 16000 bytes; 24 kHz output, 1s is 48000 bytes
	in := base64.StdEncoding.EncodeToString(make([]byte, 16000))
	out := base64.StdEncoding.EncodeToString(make([]byte, 48000))

	s.onServerEvent([]byte(`{"type":"session.created","session":{"id":"sess_1","audio":{"input":{"format":{"rate":16000}},"output":{"format":{"rate":24000}}}}}`))
	s.onClientEvent([]byte(`{"type":"input_audio_buffer.append","audio":"` + in + `"}`))
	s.onClientEvent([]byte(`{"type":"conversation.item.create","item":{"type":"message","role":"user"}}`))
	s.onServerEvent([]byte(`{"type":"response.output_audio.delta","response_id":"r1","delta":"` + out + `"}`))
	clock = clock.Add(2 * time.Second)
	s.onServerEvent([]byte(`{"type":"response.done","response":{"id":"r1","status":"completed","usage":{"output_tokens":100}}}`))
	s.onServerEvent([]byte(`{"type":"response.done","response":{"id":"r2","status":"cancelled","usage":{"output_tokens":4}}}`))
	s.onServerEvent([]byte(`{"type":"error","error":{"message":"the buffer is empty"}}`))

	var tm ActivityLogEntry
	s.fill(&tm)

	if tm.Tokens.OutputTokens != 104 {
		t.Errorf("output tokens = %d, want 104", tm.Tokens.OutputTokens)
	}
	if tm.Tokens.TokensPerSecond != 50 {
		t.Errorf("decode rate = %v, want 50 (100 tokens over 2s; r2 had no output)", tm.Tokens.TokensPerSecond)
	}
	if tm.Tokens.PromptPerSecond != -1 {
		t.Errorf("prefill rate = %v, want -1 (not reported)", tm.Tokens.PromptPerSecond)
	}
	want := map[string]string{
		"session": "sess_1", "replies": "2", "interrupted": "1",
		"audio_in": "0.5s", "audio_out": "1.0s", "text_messages": "1",
		"errors": "1", "last_error": "the buffer is empty",
	}
	for k, v := range want {
		if tm.Metadata[k] != v {
			t.Errorf("metadata[%s] = %q, want %q", k, tm.Metadata[k], v)
		}
	}
}

// TestServer_Realtime_RecordsSessionActivity runs a session through the
// full middleware chain and checks that one activity row is filed when the
// websocket closes.
func TestServer_Realtime_RecordsSessionActivity(t *testing.T) {
	audio := base64.StdEncoding.EncodeToString(make([]byte, 32000)) // 1s at 16 kHz
	clientFrames := append(
		textFrame(true, `{"type":"input_audio_buffer.append","audio":"`+audio+`"}`),
		textFrame(true, `{"type":"input_audio_buffer.commit"}`)...,
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, brw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("upstream hijack: %v", err)
			return
		}
		defer conn.Close()
		fmt.Fprint(brw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		brw.Write(textFrame(false, `{"type":"session.created","session":{"id":"sess_e2e"}}`))
		brw.Flush()
		// wait for the client's audio, then answer and hang up
		if _, err := io.ReadFull(brw, make([]byte, len(clientFrames))); err != nil {
			t.Errorf("upstream read: %v", err)
			return
		}
		brw.Write(textFrame(false, `{"type":"response.output_text.delta","response_id":"r1","delta":"hi"}`))
		brw.Write(textFrame(false, `{"type":"response.done","response":{"id":"r1","status":"completed","usage":{"output_tokens":7}}}`))
		brw.Flush()
	}))
	defer upstream.Close()

	target, _ := url.Parse(upstream.URL)
	local := newStubRouter([]string{"rt-model"}, "")
	local.serveHTTP = httputil.NewSingleHostReverseProxy(target).ServeHTTP
	s := newTestServer(local, newStubRouter(nil, ""))
	front := httptest.NewServer(s)
	defer front.Close()

	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(front.URL, "http://"), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(conn, "GET /v1/realtime?model=rt-model HTTP/1.1\r\n"+
		"Host: test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n"+
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: status %v, err %v", resp, err)
	}
	if _, err := conn.Write(clientFrames); err != nil {
		t.Fatal(err)
	}
	// read until the upstream hangs up, which ends the session
	_, _ = io.Copy(io.Discard, br)

	var entries []ActivityLogEntry
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if entries = metricsEntries(t, s.metrics); len(entries) > 0 {
			break
		}
	}
	if len(entries) != 1 {
		t.Fatalf("activity rows = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Task != "realtime" || e.Model != "rt-model" || e.ReqPath != "/v1/realtime" || e.RespStatusCode != http.StatusSwitchingProtocols {
		t.Errorf("row = task %q model %q path %q status %d", e.Task, e.Model, e.ReqPath, e.RespStatusCode)
	}
	if e.Tokens.OutputTokens != 7 {
		t.Errorf("output tokens = %d, want 7", e.Tokens.OutputTokens)
	}
	if e.Metadata["session"] != "sess_e2e" || e.Metadata["replies"] != "1" || e.Metadata["audio_in"] != "1.0s" {
		t.Errorf("metadata = %v", e.Metadata)
	}
}
