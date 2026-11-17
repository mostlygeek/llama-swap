package server

import (
	"bufio"
	"bytes"
	"context"
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

// realtimeControlWriteWait bounds a pong or close reply to the client, so a
// client that never reads cannot block the reader that answers it.
var realtimeControlWriteWait = 5 * time.Second

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

	cr := &clientReader{conn: conn}
	// The server's reader may already hold the first frames. Read the
	// connection itself after that: a read error through the server's reader,
	// including the deadline stop() uses, cancels the request's context.
	var held []byte
	if n := brw.Reader.Buffered(); n > 0 {
		held, _ = brw.Reader.Peek(n)
	}
	model, err := cr.waitForModel(held, realtimeModelWait)
	if err != nil {
		cr.report(err)
		return
	}

	// Until the upstream takes the connection, which can wait on a queue or
	// a model load, the client is still read in the background: pings are
	// answered, and a close frame or a dropped connection cancels the
	// request so it gives up its place.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := cr.watch(cancel)
	defer stop()

	up := r.Clone(swaputil.SetClientContext(ctx, ctx))
	q := up.URL.Query()
	q.Set("model", model)
	up.URL.RawQuery = q.Encode()
	up.RequestURI = up.URL.RequestURI()
	// Nothing was negotiated with the client, so the upstream must not
	// agree to compress either.
	up.Header.Del("Sec-WebSocket-Extensions")

	dw := &deferredUpgradeWriter{
		header: make(http.Header),
		takeover: func() net.Conn {
			stop()
			return &replayConn{Conn: conn, prefix: cr.pending()}
		},
	}
	next.ServeHTTP(dw, up)
	if dw.hijacked {
		return
	}
	stop()
	if cr.err != nil {
		cr.report(cr.err)
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

var (
	errRealtimeNoModel      = errors.New("no model: name it in the session.update event (session.audio.input.transcription.model or session.model) or with ?model= on the URL")
	errRealtimeTooLarge     = errors.New("message too large")
	errRealtimeClientClosed = errors.New("client closed the websocket")
)

// maxRealtimePending caps the client frames held for replay while the
// request waits for its model. Past it the client is no longer read, so TCP
// pushes back until the upstream takes over.
const maxRealtimePending = 16 << 20

// clientReader reads the client's side of a deferred realtime connection
// before the upstream exists. Data frames are kept, still masked, to be
// replayed upstream; control frames are answered here, since nothing else
// would: a ping gets a pong and a close frame is echoed.
type clientReader struct {
	conn net.Conn

	raw  []byte   // bytes of an incomplete frame
	out  []byte   // complete data frames, for replay
	text wsReader // reassembles text messages from out

	model   string
	noModel bool  // a session update named no model
	err     error // why the background reader stopped, if not stop()

	// mu orders reply's write deadline against stop's, so a pong being
	// written cannot push back the deadline stop uses to end it.
	mu       sync.Mutex
	stopping bool
}

// read reads once from the connection and handles the frames it completes.
func (c *clientReader) read(chunk []byte) error {
	n, err := c.conn.Read(chunk)
	if n > 0 {
		if ferr := c.consume(chunk[:n]); ferr != nil {
			return ferr
		}
	}
	return err
}

func (c *clientReader) consume(b []byte) error {
	c.raw = append(c.raw, b...)
	for {
		hl, n, ok := frameSize(c.raw)
		if !ok {
			return nil
		}
		if n > maxRealtimeMessage {
			return errRealtimeTooLarge
		}
		if uint64(len(c.raw)-hl) < n {
			return nil
		}
		frame := c.raw[:hl+int(n)]
		switch frame[0] & 0x0f {
		case 0x9: // ping
			if err := c.reply(serverFrame(0xa, unmaskPayload(frame, hl))); err != nil {
				return err
			}
		case 0xa: // unsolicited pong
		case 0x8: // close: echo its status code and stop
			payload := unmaskPayload(frame, hl)
			if len(payload) > 2 {
				payload = payload[:2]
			}
			_ = c.reply(serverFrame(0x8, payload))
			return errRealtimeClientClosed
		default:
			c.out = append(c.out, frame...)
			c.text.feed(frame)
		}
		c.raw = append(c.raw[:0], c.raw[len(frame):]...)
	}
}

// reply writes a control frame to the client within realtimeControlWriteWait.
func (c *clientReader) reply(frame []byte) error {
	c.mu.Lock()
	if c.stopping {
		c.mu.Unlock()
		return net.ErrClosed
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(realtimeControlWriteWait))
	c.mu.Unlock()

	_, err := c.conn.Write(frame)

	c.mu.Lock()
	if !c.stopping {
		_ = c.conn.SetWriteDeadline(time.Time{})
	}
	c.mu.Unlock()
	return err
}

func (c *clientReader) isStopping() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopping
}

// waitForModel reads until a session update names a model, starting with
// held, the bytes already read from the connection.
func (c *clientReader) waitForModel(held []byte, timeout time.Duration) (string, error) {
	c.text.onText = func(msg []byte) {
		if c.model != "" || c.noModel {
			return
		}
		m, isUpdate := realtimeEventModel(msg)
		switch {
		case m != "":
			c.model = m
		case isUpdate:
			c.noModel = true
		}
	}

	_ = c.conn.SetReadDeadline(time.Now().Add(timeout))
	defer c.conn.SetReadDeadline(time.Time{})

	chunk := make([]byte, 32<<10)
	var err error
	if len(held) > 0 {
		err = c.consume(held)
	}
	for {
		if err == nil && c.model == "" && !c.noModel {
			err = c.read(chunk)
		}
		switch {
		case c.model != "":
			return c.model, nil
		case c.noModel, c.text.broken, len(c.out) > maxRealtimeMessage:
			return "", errRealtimeNoModel
		}
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return "", errRealtimeNoModel
			}
			return "", err
		}
	}
}

// watch keeps reading the client in the background, calling cancel when it
// closes the websocket or drops the connection. The returned stop ends the
// reading and waits for it; it may be called more than once.
func (c *clientReader) watch(cancel context.CancelFunc) (stop func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		chunk := make([]byte, 32<<10)
		for len(c.out) < maxRealtimePending {
			if err := c.read(chunk); err != nil {
				if !c.isStopping() {
					c.err = err
					cancel()
				}
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			// Both directions: the reader may be blocked writing a pong.
			c.mu.Lock()
			c.stopping = true
			_ = c.conn.SetDeadline(time.Now())
			c.mu.Unlock()
			<-done
			_ = c.conn.SetDeadline(time.Time{})
		})
	}
}

// pending returns every client byte read but not yet sent upstream.
func (c *clientReader) pending() []byte {
	return append(c.out, c.raw...)
}

// report tells the client why its connection is ending, where it can still
// be told.
func (c *clientReader) report(err error) {
	switch {
	case errors.Is(err, errRealtimeNoModel):
		closeRealtime(c.conn, 1008, "invalid_request_error", "model_not_found", err.Error())
	case errors.Is(err, errRealtimeTooLarge):
		closeRealtime(c.conn, 1009, "invalid_request_error", "", err.Error())
	}
}

// frameSize parses a websocket frame header: its length, and the payload's.
func frameSize(b []byte) (hl int, n uint64, ok bool) {
	if len(b) < 2 {
		return 0, 0, false
	}
	n = uint64(b[1] & 0x7f)
	hl = 2
	switch n {
	case 126:
		if len(b) < 4 {
			return 0, 0, false
		}
		n = uint64(binary.BigEndian.Uint16(b[2:4]))
		hl = 4
	case 127:
		if len(b) < 10 {
			return 0, 0, false
		}
		n = binary.BigEndian.Uint64(b[2:10])
		hl = 10
	}
	if b[1]&0x80 != 0 {
		hl += 4
	}
	if len(b) < hl {
		return 0, 0, false
	}
	return hl, n, true
}

// unmaskPayload returns a copy of a complete frame's payload, unmasked.
func unmaskPayload(frame []byte, hl int) []byte {
	payload := append([]byte(nil), frame[hl:]...)
	if frame[1]&0x80 != 0 {
		key := frame[hl-4 : hl]
		for i := range payload {
			payload[i] ^= key[i%4]
		}
	}
	return payload
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
	// takeover stops the background client reader and returns the
	// connection for the upstream proxy.
	takeover func() net.Conn
}

func (w *deferredUpgradeWriter) Header() http.Header { return w.header }

func (w *deferredUpgradeWriter) WriteHeader(code int) {
	if swaputil.IsInformationalStatus(code) {
		return
	}
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
	conn := w.takeover()
	return conn, bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn)), nil
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
