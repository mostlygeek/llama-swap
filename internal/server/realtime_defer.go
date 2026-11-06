package server

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/internal/swaputil"
	"github.com/tidwall/gjson"
)

// OpenAI's Realtime transcription guide has clients connect to
// /v1/realtime?intent=transcription with no model in the URL, and name the
// model afterwards in a session.update event
// (session.audio.input.transcription.model). llama-swap routes on the model
// before it connects upstream, so for such a connection it answers the
// websocket handshake itself, reads the client's events until one names a
// model, and only then dispatches the request through the model chain with
// ?model= added. The events read so far are replayed to the upstream ahead
// of the rest of the stream, and the upstream's own 101 response is dropped
// since the client already has one.

// realtimeModelWait bounds how long a connection without ?model= may stay
// open before its session.update names a model.
var realtimeModelWait = 30 * time.Second

// websocketGUID is the RFC 6455 handshake constant.
const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// deferRealtimeModel wraps the /v1/realtime handler. Requests that name their
// model in the query, and anything that is not a websocket upgrade, go
// straight to next. auth guards the deferred path, which otherwise would
// accept the websocket before any middleware in next has seen it.
func deferRealtimeModel(auth func(http.Handler) http.Handler, next http.Handler) http.Handler {
	deferred := auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveRealtimeDeferred(w, r, next)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !swaputil.IsWebSocketUpgrade(r) || r.URL.Query().Get("model") != "" {
			next.ServeHTTP(w, r)
			return
		}
		deferred.ServeHTTP(w, r)
	})
}

// realtimeEventModel returns the model a client event names for the session,
// and whether the event is a session update at all. session.model is used
// when present, otherwise the input transcription model, in both the GA and
// the beta transcription_session shapes.
func realtimeEventModel(msg []byte) (model string, isUpdate bool) {
	switch gjson.GetBytes(msg, "type").Str {
	case "session.update", "transcription_session.update":
	default:
		return "", false
	}
	for _, path := range []string{
		"session.model",
		"session.audio.input.transcription.model",
		"session.input_audio_transcription.model",
	} {
		if m := gjson.GetBytes(msg, path).Str; m != "" {
			return m, true
		}
	}
	return "", true
}

func serveRealtimeDeferred(w http.ResponseWriter, r *http.Request, next http.Handler) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" || r.Header.Get("Sec-WebSocket-Version") != "13" {
		swaputil.SendResponse(w, r, http.StatusBadRequest, "invalid websocket handshake")
		return
	}

	conn, brw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		swaputil.SendResponse(w, r, http.StatusInternalServerError, "websocket upgrade not supported")
		return
	}
	defer conn.Close()
	if marker, ok := w.(swaputil.StatusMarker); ok {
		marker.MarkStatus(http.StatusSwitchingProtocols)
	}

	sum := sha1.Sum([]byte(key + websocketGUID))
	head := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n"
	// No extension is negotiated, and the first offered subprotocol is
	// accepted; OpenAI's browser clients offer "realtime" first.
	if proto := firstToken(r.Header.Values("Sec-WebSocket-Protocol")); proto != "" {
		head += "Sec-WebSocket-Protocol: " + proto + "\r\n"
	}
	if _, err := io.WriteString(conn, head+"\r\n"); err != nil {
		return
	}

	model, buffered, err := readRealtimeModel(conn, brw.Reader)
	if err != nil {
		if errors.Is(err, errRealtimeNoModel) {
			closeRealtime(conn, 1008, "invalid_request_error", "model_not_found", err.Error())
		}
		return
	}

	up := r.Clone(r.Context())
	q := up.URL.Query()
	q.Set("model", model)
	up.URL.RawQuery = q.Encode()
	up.RequestURI = up.URL.RequestURI()
	// Nothing was negotiated with the client, so the upstream must not
	// agree to compress either.
	up.Header.Del("Sec-WebSocket-Extensions")

	dw := &deferredUpgradeWriter{
		header: make(http.Header),
		conn:   &replayConn{Conn: conn, prefix: buffered},
	}
	next.ServeHTTP(dw, up)
	if dw.hijacked {
		return
	}
	// The upstream never switched protocols: report its error on the
	// websocket the client already has.
	msg := gjson.GetBytes(dw.body.Bytes(), "error.message").Str
	if msg == "" {
		msg = strings.TrimSpace(dw.body.String())
	}
	if msg == "" {
		msg = http.StatusText(dw.status)
	}
	closeRealtime(conn, 1011, "server_error", "", fmt.Sprintf("model %q: %s", model, msg))
}

var errRealtimeNoModel = errors.New("no model: name it in the session.update event (session.audio.input.transcription.model or session.model) or with ?model= on the URL")

// readRealtimeModel reads client frames until a session update names a
// model. It returns the model and every byte read, which must be replayed to
// the upstream.
func readRealtimeModel(conn net.Conn, br *bufio.Reader) (string, []byte, error) {
	var (
		buffered []byte
		model    string
		found    bool
		failed   error
	)
	ws := wsReader{onText: func(msg []byte) {
		if found || failed != nil {
			return
		}
		m, isUpdate := realtimeEventModel(msg)
		switch {
		case m != "":
			model, found = m, true
		case isUpdate:
			failed = errRealtimeNoModel
		}
	}}

	_ = conn.SetReadDeadline(time.Now().Add(realtimeModelWait))
	defer conn.SetReadDeadline(time.Time{})

	chunk := make([]byte, 32<<10)
	for !found {
		n, err := br.Read(chunk)
		if n > 0 {
			buffered = append(buffered, chunk[:n]...)
			ws.feed(chunk[:n])
		}
		if failed != nil {
			return "", nil, failed
		}
		if found {
			break
		}
		if ws.broken || len(buffered) > maxRealtimeMessage {
			return "", nil, errRealtimeNoModel
		}
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return "", nil, errRealtimeNoModel
			}
			return "", nil, err
		}
	}
	// Hand over anything the reader still holds, since the proxy reads the
	// connection itself from here on.
	if n := br.Buffered(); n > 0 {
		rest, _ := br.Peek(n)
		buffered = append(buffered, rest...)
	}
	return model, buffered, nil
}

// closeRealtime sends an OpenAI style error event and a close frame.
func closeRealtime(conn net.Conn, code uint16, errType, errCode, message string) {
	ev := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    errType,
			"code":    errCode,
			"message": message,
		},
	}
	payload, _ := json.Marshal(ev)
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = conn.Write(serverFrame(0x1, payload))
	reason := binary.BigEndian.AppendUint16(nil, code)
	_, _ = conn.Write(serverFrame(0x8, reason))
}

// serverFrame builds one unmasked, final websocket frame.
func serverFrame(op byte, payload []byte) []byte {
	frame := []byte{0x80 | op}
	switch n := len(payload); {
	case n < 126:
		frame = append(frame, byte(n))
	case n <= 0xffff:
		frame = append(frame, 126)
		frame = binary.BigEndian.AppendUint16(frame, uint16(n))
	default:
		frame = append(frame, 127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(n))
	}
	return append(frame, payload...)
}

func firstToken(values []string) string {
	for _, v := range values {
		for part := range strings.SplitSeq(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				return p
			}
		}
	}
	return ""
}

// deferredUpgradeWriter is the response writer the model chain sees for a
// deferred realtime connection. A successful upgrade hijacks it and gets the
// client connection; anything else is an error response, kept to be relayed
// as a websocket error event.
type deferredUpgradeWriter struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	hijacked bool
	conn     *replayConn
}

func (w *deferredUpgradeWriter) Header() http.Header { return w.header }

func (w *deferredUpgradeWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

func (w *deferredUpgradeWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len() < 64<<10 {
		w.body.Write(b)
	}
	return len(b), nil
}

func (w *deferredUpgradeWriter) Flush() {}

func (w *deferredUpgradeWriter) MarkStatus(code int) { w.status = code }
func (w *deferredUpgradeWriter) WroteHeader() bool   { return w.status != 0 || w.hijacked }

func (w *deferredUpgradeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if w.hijacked {
		return nil, nil, http.ErrHijacked
	}
	w.hijacked = true
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}

// replayConn is the client connection handed to the upstream proxy. Reads
// return the frames buffered while waiting for the model before the live
// stream, and the upstream's HTTP 101 response head is dropped from writes.
type replayConn struct {
	net.Conn

	mu       sync.Mutex
	prefix   []byte
	headDone bool
	headTail []byte // trailing bytes of the head seen so far, to find \r\n\r\n
}

func (c *replayConn) Read(b []byte) (int, error) {
	c.mu.Lock()
	if len(c.prefix) > 0 {
		n := copy(b, c.prefix)
		c.prefix = c.prefix[n:]
		c.mu.Unlock()
		return n, nil
	}
	c.mu.Unlock()
	return c.Conn.Read(b)
}

func (c *replayConn) Write(b []byte) (int, error) {
	total := len(b)
	if !c.headDone {
		c.headTail = append(c.headTail, b...)
		i := bytes.Index(c.headTail, []byte("\r\n\r\n"))
		if i < 0 {
			if len(c.headTail) > 3 {
				c.headTail = c.headTail[len(c.headTail)-3:]
			}
			return total, nil
		}
		c.headDone = true
		b = c.headTail[i+4:]
		c.headTail = nil
		if len(b) == 0 {
			return total, nil
		}
	}
	if _, err := c.Conn.Write(b); err != nil {
		return 0, err
	}
	return total, nil
}
