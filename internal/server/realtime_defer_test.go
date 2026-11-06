package server

import (
	"bufio"
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

	"github.com/tidwall/gjson"
)

// readServerFrame reads one unmasked, unfragmented websocket frame.
func readServerFrame(t *testing.T, br *bufio.Reader) (byte, []byte) {
	t.Helper()
	var h [2]byte
	if _, err := io.ReadFull(br, h[:]); err != nil {
		t.Fatalf("read frame header: %v", err)
	}
	n := uint64(h[1] & 0x7f)
	switch n {
	case 126:
		var b [2]byte
		io.ReadFull(br, b[:])
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		io.ReadFull(br, b[:])
		n = binary.BigEndian.Uint64(b[:])
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(br, payload); err != nil {
		t.Fatalf("read frame payload: %v", err)
	}
	return h[0] & 0x0f, payload
}

// dialRealtime opens a websocket to front at path and checks the handshake
// llama-swap answers itself.
func dialRealtime(t *testing.T, front *httptest.Server, path, extraHeaders string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(front.URL, "http://"), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(conn, "GET "+path+" HTTP/1.1\r\n"+
		"Host: test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n"+
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"+
		extraHeaders+"\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	// RFC 6455's worked example key and accept value.
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Errorf("Sec-WebSocket-Accept = %q", got)
	}
	return conn, br
}

// TestServer_Realtime_DeferredModelFromSessionUpdate connects the way OpenAI's
// transcription guide says to, with no model in the URL, and checks that the
// session.update event's transcription model picks the upstream and reaches
// it, followed by the live stream in both directions.
func TestServer_Realtime_DeferredModelFromSessionUpdate(t *testing.T) {
	update := `{"type":"session.update","session":{"type":"transcription","audio":{"input":{"transcription":{"model":"rt-model"}}}}}`
	after := `{"type":"input_audio_buffer.commit"}`

	type seen struct {
		query, extensions string
		frames            []byte
	}
	gotReq := make(chan seen, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, brw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("upstream hijack: %v", err)
			return
		}
		defer conn.Close()
		fmt.Fprint(brw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Extensions: permessage-deflate\r\n\r\n")
		brw.Flush()
		want := len(textFrame(true, update)) + len(textFrame(true, after))
		frames := make([]byte, want)
		if _, err := io.ReadFull(brw, frames); err != nil {
			t.Errorf("upstream read: %v", err)
			return
		}
		gotReq <- seen{r.URL.RawQuery, r.Header.Get("Sec-WebSocket-Extensions"), frames}
		brw.Write(textFrame(false, `{"type":"conversation.item.input_audio_transcription.completed","transcript":"hi"}`))
		brw.Flush()
	}))
	defer upstream.Close()

	target, _ := url.Parse(upstream.URL)
	local := newStubRouter([]string{"rt-model"}, "")
	local.serveHTTP = httputil.NewSingleHostReverseProxy(target).ServeHTTP
	front := httptest.NewServer(newTestServer(local, newStubRouter(nil, "")))
	defer front.Close()

	conn, br := dialRealtime(t, front, "/v1/realtime?intent=transcription",
		"Sec-WebSocket-Extensions: permessage-deflate\r\n")
	conn.Write(textFrame(true, update))
	conn.Write(textFrame(true, after))

	got := <-gotReq
	q, _ := url.ParseQuery(got.query)
	if q.Get("model") != "rt-model" || q.Get("intent") != "transcription" {
		t.Errorf("upstream query = %q, want model=rt-model and intent=transcription", got.query)
	}
	if got.extensions != "" {
		t.Errorf("upstream was offered extensions %q, want none", got.extensions)
	}
	wantFrames := string(textFrame(true, update)) + string(textFrame(true, after))
	if string(got.frames) != wantFrames {
		t.Errorf("upstream frames were not replayed unchanged")
	}

	// the next bytes are the upstream's event, not its 101 response head
	op, payload := readServerFrame(t, br)
	if op != 0x1 || gjson.GetBytes(payload, "transcript").Str != "hi" {
		t.Errorf("frame op %d payload %q", op, payload)
	}
}

func TestServer_Realtime_DeferredUnknownModelReportsError(t *testing.T) {
	front := httptest.NewServer(newTestServer(newStubRouter([]string{"rt-model"}, ""), newStubRouter(nil, "")))
	defer front.Close()

	conn, br := dialRealtime(t, front, "/v1/realtime?intent=transcription", "")
	conn.Write(textFrame(true, `{"type":"session.update","session":{"audio":{"input":{"transcription":{"model":"nope"}}}}}`))

	op, payload := readServerFrame(t, br)
	if op != 0x1 || gjson.GetBytes(payload, "type").Str != "error" ||
		!strings.Contains(gjson.GetBytes(payload, "error.message").Str, "nope") {
		t.Errorf("frame op %d payload %q, want an error event naming the model", op, payload)
	}
	if op, _ := readServerFrame(t, br); op != 0x8 {
		t.Errorf("op = %d, want a close frame", op)
	}
}

func TestServer_Realtime_DeferredSessionUpdateWithoutModel(t *testing.T) {
	front := httptest.NewServer(newTestServer(newStubRouter([]string{"rt-model"}, ""), newStubRouter(nil, "")))
	defer front.Close()

	conn, br := dialRealtime(t, front, "/v1/realtime", "")
	conn.Write(textFrame(true, `{"type":"session.update","session":{"type":"transcription"}}`))

	op, payload := readServerFrame(t, br)
	if op != 0x1 || gjson.GetBytes(payload, "error.code").Str != "model_not_found" {
		t.Errorf("frame op %d payload %q, want a model_not_found error event", op, payload)
	}
	op, payload = readServerFrame(t, br)
	if op != 0x8 || binary.BigEndian.Uint16(payload) != 1008 {
		t.Errorf("close frame op %d payload %v, want 1008", op, payload)
	}
}

func TestServer_Realtime_DeferredTimesOutWithoutModel(t *testing.T) {
	old := realtimeModelWait
	realtimeModelWait = 50 * time.Millisecond
	defer func() { realtimeModelWait = old }()

	front := httptest.NewServer(newTestServer(newStubRouter([]string{"rt-model"}, ""), newStubRouter(nil, "")))
	defer front.Close()

	_, br := dialRealtime(t, front, "/v1/realtime", "")
	op, payload := readServerFrame(t, br)
	if op != 0x1 || gjson.GetBytes(payload, "type").Str != "error" {
		t.Errorf("frame op %d payload %q, want an error event", op, payload)
	}
}

func TestServer_Realtime_EventModel(t *testing.T) {
	cases := []struct {
		msg      string
		model    string
		isUpdate bool
	}{
		{`{"type":"session.update","session":{"model":"a","audio":{"input":{"transcription":{"model":"b"}}}}}`, "a", true},
		{`{"type":"session.update","session":{"audio":{"input":{"transcription":{"model":"b"}}}}}`, "b", true},
		{`{"type":"transcription_session.update","session":{"input_audio_transcription":{"model":"c"}}}`, "c", true},
		{`{"type":"session.update","session":{}}`, "", true},
		{`{"type":"input_audio_buffer.append","audio":""}`, "", false},
	}
	for _, c := range cases {
		m, u := realtimeEventModel([]byte(c.msg))
		if m != c.model || u != c.isUpdate {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.msg, m, u, c.model, c.isUpdate)
		}
	}
}

// TestServer_Realtime_DeferredAnswersPingAndClose checks that control frames
// sent before any upstream exists are answered by llama-swap.
func TestServer_Realtime_DeferredAnswersPingAndClose(t *testing.T) {
	front := httptest.NewServer(newTestServer(newStubRouter([]string{"rt-model"}, ""), newStubRouter(nil, "")))
	defer front.Close()

	conn, br := dialRealtime(t, front, "/v1/realtime", "")
	conn.Write(wsFrame(0x9, true, true, []byte("hello")))
	if op, payload := readServerFrame(t, br); op != 0xa || string(payload) != "hello" {
		t.Errorf("frame op %d payload %q, want a pong echoing the ping", op, payload)
	}

	conn.Write(wsFrame(0x8, true, true, binary.BigEndian.AppendUint16(nil, 1000)))
	op, payload := readServerFrame(t, br)
	if op != 0x8 || len(payload) != 2 || binary.BigEndian.Uint16(payload) != 1000 {
		t.Errorf("frame op %d payload %v, want a close frame echoing 1000", op, payload)
	}
	if _, err := br.ReadByte(); err != io.EOF {
		t.Errorf("read after close = %v, want EOF", err)
	}
}

// TestServer_Realtime_DeferredDisconnectCancelsRequest checks that a client
// hanging up while its request waits on the model cancels the request, so it
// does not hold a queue slot or start a swap for nobody.
func TestServer_Realtime_DeferredDisconnectCancelsRequest(t *testing.T) {
	waiting := make(chan struct{})
	canceled := make(chan struct{})
	local := newStubRouter([]string{"rt-model"}, "")
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		close(waiting)
		<-r.Context().Done()
		close(canceled)
	}
	front := httptest.NewServer(newTestServer(local, newStubRouter(nil, "")))
	defer front.Close()

	conn, br := dialRealtime(t, front, "/v1/realtime", "")
	conn.Write(textFrame(true, `{"type":"session.update","session":{"model":"rt-model"}}`))
	<-waiting

	// still answered while the request waits
	conn.Write(wsFrame(0x9, true, true, []byte("p")))
	if op, _ := readServerFrame(t, br); op != 0xa {
		t.Errorf("op = %d, want a pong while waiting", op)
	}

	conn.Close()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("request was not canceled after the client disconnected")
	}
}

// TestServer_Realtime_DeferredPingToNonReaderDoesNotBlock checks that a
// client sending pings without reading the pongs cannot leave the reader
// stuck in a write: the wait for a model gives up within the control write
// bound, and stop() releases a background reader at once.
func TestServer_Realtime_DeferredPingToNonReaderDoesNotBlock(t *testing.T) {
	old := realtimeControlWriteWait
	realtimeControlWriteWait = 50 * time.Millisecond
	defer func() { realtimeControlWriteWait = old }()

	// net.Pipe has no buffer, so a pong nobody reads blocks its write.
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	go client.Write(wsFrame(0x9, true, true, []byte("p")))

	cr := &clientReader{conn: server}
	done := make(chan error, 1)
	go func() {
		_, err := cr.waitForModel(nil, time.Minute)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("waitForModel succeeded, want an error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waitForModel blocked on a pong the client never read")
	}

	// stop() must release a background reader blocked the same way, even
	// before the write bound expires.
	realtimeControlWriteWait = time.Minute
	server2, client2 := net.Pipe()
	defer server2.Close()
	defer client2.Close()
	cr2 := &clientReader{conn: server2}
	stop := cr2.watch(func() {})
	if _, err := client2.Write(wsFrame(0x9, true, true, []byte("p"))); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("stop() blocked on a pong the client never read")
	}
}
