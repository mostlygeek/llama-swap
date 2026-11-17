package server

import (
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

// Realtime sessions (/v1/realtime) are websockets: once the upgrade is done
// httputil.ReverseProxy copies raw bytes between the client and the upstream,
// and no response body ever reaches the metrics recorder. To still file an
// activity row, the hijacked client connection is wrapped so both directions
// pass through a websocket frame reader, which hands each JSON event to a
// realtimeSession that tallies the session. The row is written when the
// socket closes.
//
// The tap only reads; it never changes, delays or drops a byte. Anything it
// cannot follow (an unknown opcode, a message over maxRealtimeMessage) is
// skipped and the bytes still flow.

// maxRealtimeMessage caps one buffered websocket message. Realtime events are
// small (an audio chunk is a few KB of base64); anything larger is skipped
// rather than held in memory.
const maxRealtimeMessage = 4 << 20

// wsReader reassembles websocket text messages from a byte stream, as seen
// in one direction of a connection. It is fed in whatever pieces the
// connection delivers and calls onText once per complete text message. The
// slice passed to onText is only valid during the call.
type wsReader struct {
	onText func([]byte)

	buf    []byte // unparsed bytes
	msg    []byte // text message being assembled across fragments
	inText bool   // inside a text message
	drop   bool   // current message is over the cap, discard it
	broken bool   // stream could not be followed, stop parsing

	// skip counts payload bytes still to discard from an oversized frame,
	// whose fin/opcode are kept to close the message once it is consumed.
	skip    uint64
	skipFin bool
	skipOp  byte
}

func (p *wsReader) feed(b []byte) {
	for len(b) > 0 && !p.broken {
		if p.skip > 0 {
			n := min(p.skip, uint64(len(b)))
			b = b[n:]
			p.skip -= n
			if p.skip == 0 {
				p.endFrame(p.skipFin, p.skipOp, nil)
			}
			continue
		}
		p.buf = append(p.buf, b...)
		b = nil
		b = p.parse()
	}
}

// parse consumes complete frames from p.buf. When an oversized frame starts
// it switches to skip mode and returns the bytes that followed its header,
// which feed then discards.
func (p *wsReader) parse() []byte {
	for !p.broken {
		if len(p.buf) < 2 {
			return nil
		}
		fin := p.buf[0]&0x80 != 0
		op := p.buf[0] & 0x0f
		masked := p.buf[1]&0x80 != 0
		n := uint64(p.buf[1] & 0x7f)
		hl := 2
		switch n {
		case 126:
			if len(p.buf) < 4 {
				return nil
			}
			n = uint64(binary.BigEndian.Uint16(p.buf[2:4]))
			hl = 4
		case 127:
			if len(p.buf) < 10 {
				return nil
			}
			n = binary.BigEndian.Uint64(p.buf[2:10])
			hl = 10
		}
		if masked {
			hl += 4
		}
		if len(p.buf) < hl {
			return nil
		}

		if n > maxRealtimeMessage {
			rest := append([]byte(nil), p.buf[hl:]...)
			p.buf = p.buf[:0]
			p.skip, p.skipFin, p.skipOp = n, fin, op
			return rest
		}
		if uint64(len(p.buf)-hl) < n {
			return nil
		}

		// Unmasked in place: these bytes are the tap's own copy, already
		// forwarded, so nothing else sees the change. endFrame copies what
		// it keeps into p.msg, so p.buf and p.msg are the only buffers and
		// both are reused for the life of the connection.
		payload := p.buf[hl : hl+int(n)]
		if masked {
			key := p.buf[hl-4 : hl]
			for i := range payload {
				payload[i] ^= key[i%4]
			}
		}
		p.endFrame(fin, op, payload)
		p.buf = append(p.buf[:0], p.buf[hl+int(n):]...)
	}
	return nil
}

// endFrame applies one frame. A nil payload is a frame that was skipped for
// size, which drops the message it belongs to.
func (p *wsReader) endFrame(fin bool, op byte, payload []byte) {
	switch op {
	case 0x8, 0x9, 0xa: // close, ping, pong: may sit between fragments
		return
	case 0x1: // text
		p.inText, p.drop, p.msg = true, false, p.msg[:0]
	case 0x2: // binary, not used by the realtime API
		p.inText = false
	case 0x0: // continuation
	default:
		p.broken = true
		return
	}
	if p.inText {
		if payload == nil || len(p.msg)+len(payload) > maxRealtimeMessage {
			p.drop = true
		} else if !p.drop {
			p.msg = append(p.msg, payload...)
		}
	}
	if fin {
		if p.inText && !p.drop {
			p.onText(p.msg)
		}
		p.inText, p.drop, p.msg = false, false, p.msg[:0]
	}
}

// realtimeSession tallies one realtime websocket from the events it carries.
// The two directions are read on different goroutines, so it is locked.
type realtimeSession struct {
	mu sync.Mutex

	client wsReader // client to upstream: masked frames
	server wsReader // upstream to client

	sessionID string
	// PCM16 rates from session.created/updated, used to turn audio bytes
	// into seconds.
	inRate, outRate int
	audioInBytes    int64
	audioOutBytes   int64
	textMessages    int

	responses    int
	interrupted  int
	failed       int
	outputTokens int
	// decode sums, per reply, the time from its first output to
	// response.done: generation time, with the prompt's processing left out.
	// decodeTokens is the output of just those timed replies.
	decode       time.Duration
	decodeTokens int
	firstDelta   map[string]time.Time

	errors    int
	lastError string

	now func() time.Time
}

func newRealtimeSession() *realtimeSession {
	s := &realtimeSession{
		inRate:     16000,
		outRate:    24000,
		firstDelta: make(map[string]time.Time),
		now:        time.Now,
	}
	s.client.onText = s.onClientEvent
	s.server.onText = s.onServerEvent
	return s
}

// base64Bytes is the decoded length of a base64 string, without decoding it.
func base64Bytes(s string) int64 {
	n := int64(len(s)) / 4 * 3
	for i := len(s) - 1; i >= 0 && s[i] == '='; i-- {
		n--
	}
	return max(n, 0)
}

func (s *realtimeSession) onClientEvent(msg []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch gjson.GetBytes(msg, "type").String() {
	case "input_audio_buffer.append":
		s.audioInBytes += base64Bytes(gjson.GetBytes(msg, "audio").Str)
	case "conversation.item.create":
		if gjson.GetBytes(msg, "item.role").Str == "user" {
			s.textMessages++
		}
	}
}

func (s *realtimeSession) onServerEvent(msg []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch typ := gjson.GetBytes(msg, "type").String(); typ {
	case "session.created", "session.updated":
		if id := gjson.GetBytes(msg, "session.id").Str; id != "" {
			s.sessionID = id
		}
		if r := gjson.GetBytes(msg, "session.audio.input.format.rate").Int(); r > 0 {
			s.inRate = int(r)
		}
		if r := gjson.GetBytes(msg, "session.audio.output.format.rate").Int(); r > 0 {
			s.outRate = int(r)
		}
	case "response.output_audio.delta", "response.output_text.delta", "response.output_audio_transcript.delta":
		if typ == "response.output_audio.delta" {
			s.audioOutBytes += base64Bytes(gjson.GetBytes(msg, "delta").Str)
		}
		id := gjson.GetBytes(msg, "response_id").Str
		if _, ok := s.firstDelta[id]; !ok {
			s.firstDelta[id] = s.now()
		}
	case "response.done":
		s.responses++
		switch gjson.GetBytes(msg, "response.status").Str {
		case "cancelled":
			s.interrupted++
		case "failed":
			s.failed++
		}
		tokens := int(gjson.GetBytes(msg, "response.usage.output_tokens").Int())
		s.outputTokens += tokens
		id := gjson.GetBytes(msg, "response.id").Str
		if t, ok := s.firstDelta[id]; ok {
			s.decode += s.now().Sub(t)
			s.decodeTokens += tokens
			delete(s.firstDelta, id)
		}
	case "error":
		s.errors++
		s.lastError = gjson.GetBytes(msg, "error.message").Str
	}
}

// fill writes the session's tallies into an activity row.
func (s *realtimeSession) fill(tm *ActivityLogEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// llama-server reports only output_tokens for a reply: no prompt size and
	// no timings, so the prefill rate is unknown and the decode rate is
	// measured here.
	tm.Tokens.OutputTokens = s.outputTokens
	tm.Tokens.DraftTokens = -1
	tm.Tokens.DraftAccTokens = -1
	tm.Tokens.PromptPerSecond = -1
	tm.Tokens.TokensPerSecond = -1
	if s.decode > 0 && s.decodeTokens > 0 {
		tm.Tokens.TokensPerSecond = float64(s.decodeTokens) / s.decode.Seconds()
	}

	if tm.Metadata == nil {
		tm.Metadata = make(map[string]string)
	}
	m := tm.Metadata
	if s.sessionID != "" {
		m["session"] = s.sessionID
	}
	m["replies"] = strconv.Itoa(s.responses)
	if s.interrupted > 0 {
		m["interrupted"] = strconv.Itoa(s.interrupted)
	}
	if s.failed > 0 {
		m["failed"] = strconv.Itoa(s.failed)
	}
	if s.audioInBytes > 0 {
		m["audio_in"] = audioSeconds(s.audioInBytes, s.inRate)
	}
	if s.audioOutBytes > 0 {
		m["audio_out"] = audioSeconds(s.audioOutBytes, s.outRate)
	}
	if s.textMessages > 0 {
		m["text_messages"] = strconv.Itoa(s.textMessages)
	}
	if s.errors > 0 {
		m["errors"] = strconv.Itoa(s.errors)
		m["last_error"] = s.lastError
	}
}

// audioSeconds formats mono PCM16 byte counts as seconds.
func audioSeconds(bytes int64, rate int) string {
	return fmt.Sprintf("%.1fs", float64(bytes)/2/float64(rate))
}

// realtimeConn is the hijacked client connection with both directions
// copied into the session's frame readers.
type realtimeConn struct {
	net.Conn
	session *realtimeSession
}

func (c *realtimeConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.session.client.feed(b[:n])
	}
	return n, err
}

func (c *realtimeConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.session.server.feed(b[:n])
	}
	return n, err
}
