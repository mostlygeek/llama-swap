package router

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
)

var loadingPaths = []string{
	"/v1/chat/completions",
}

func isLoadingPath(path string) bool {
	for _, p := range loadingPaths {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// IsLoadingPath reports whether path is one the loading/thinking stream applies
// to. Exported for the scheduler admission layer, which streams queue position
// on the same paths while a request waits for a slot.
func IsLoadingPath(path string) bool { return isLoadingPath(path) }

type loadingWriter struct {
	hasWritten bool
	writer     http.ResponseWriter
	req        *http.Request
	ctx        context.Context
	logger     *logmon.Monitor
	modelName  string
	startTime  time.Time

	pendingMu     sync.Mutex
	pendingUpdate string

	// writeMu serializes writes to the underlying writer and guards released.
	// Once released is set, the streaming goroutine must not touch the writer
	// again — ServeHTTP has reclaimed it (to run the real handler or to return)
	// and writing/flushing a finalized response panics.
	writeMu  sync.Mutex
	released bool

	// closed by start when the goroutine finishes (after cleanup messages)
	done chan struct{}

	// test-only: closed when start enters its loop
	loopStarted chan struct{}
	// test-only: override the 1s tick interval
	tickDuration time.Duration
	// test-only: override character streaming speed (0 = no delay)
	charPerSecond float64
}

// newStreamWriter sets up the SSE response and returns a loadingWriter with no
// banner emitted. Callers add their own headline (loading vs queued).
func newStreamWriter(logger *logmon.Monitor, modelName string, w http.ResponseWriter, req *http.Request) *loadingWriter {
	s := &loadingWriter{
		writer:        w,
		req:           req,
		ctx:           req.Context(),
		logger:        logger,
		modelName:     modelName,
		startTime:     time.Now(),
		tickDuration:  750 * time.Millisecond,
		charPerSecond: 75,
	}

	s.Header().Set("Content-Type", "text/event-stream")
	s.Header().Set("Cache-Control", "no-cache")
	s.Header().Set("Connection", "keep-alive")
	s.WriteHeader(http.StatusOK)
	return s
}

func newLoadingWriter(logger *logmon.Monitor, modelName string, w http.ResponseWriter, req *http.Request) *loadingWriter {
	s := newStreamWriter(logger, modelName, w, req)
	s.sendLine("━━━━━")
	s.sendLine(fmt.Sprintf("llama-swap loading model: %s", modelName))
	return s
}

func newWaitWriter(logger *logmon.Monitor, modelName string, w http.ResponseWriter, req *http.Request) *loadingWriter {
	s := newStreamWriter(logger, modelName, w, req)
	s.sendLine("━━━━━")
	s.sendLine(fmt.Sprintf("llama-swap queued: %s", modelName))
	return s
}

// StreamQueueWait streams a "queued" thinking block to a streaming client while
// it waits for a scheduler slot, updating it with the live queue position read
// from positions. The stream is created lazily on the first position received —
// so a request admitted immediately (no position ever sent) emits nothing and
// the caller's normal response or 429 path is preserved. The returned stop func
// ends the stream and fences the writer off the ResponseWriter; call it once the
// wait resolves, before the real handler reclaims the writer.
func StreamQueueWait(logger *logmon.Monitor, modelName string, w http.ResponseWriter, req *http.Request, positions <-chan int) (stop func()) {
	ctx, cancel := context.WithCancel(req.Context())
	done := make(chan struct{})
	var lw *loadingWriter
	go func() {
		defer close(done)
		for {
			select {
			case pos, ok := <-positions:
				if !ok {
					return
				}
				if lw == nil {
					lw = newWaitWriter(logger, modelName, w, req)
					go lw.start(ctx)
				}
				lw.setUpdate(fmt.Sprintf("Queue position: #%d", pos))
			case <-ctx.Done():
				return
			}
		}
	}()
	return func() {
		cancel()
		<-done // pump goroutine has exited; lw is now safe to read
		if lw != nil {
			lw.waitForCompletion(1 * time.Second)
			lw.release()
		}
	}
}

func (s *loadingWriter) setUpdate(msg string) {
	s.pendingMu.Lock()
	s.pendingUpdate = msg
	s.pendingMu.Unlock()
}

func (s *loadingWriter) start(ctx context.Context) {
	s.done = make(chan struct{})
	defer close(s.done)

	defer func() {
		// Skip cleanup writes if the client disconnected — the connection
		// is being torn down and flushing against it will panic.
		if s.ctx.Err() != nil {
			return
		}
		duration := time.Since(s.startTime)
		s.sendData("\n")
		s.sendLine(fmt.Sprintf("Done! (%.2fs)", duration.Seconds()))
		s.sendLine("━━━━━")
		s.sendLine(" ")
	}()

	remarks := make([]string, len(loadingRemarks))
	copy(remarks, loadingRemarks)
	rand.Shuffle(len(remarks), func(i, j int) {
		remarks[i], remarks[j] = remarks[j], remarks[i]
	})
	ri := 0

	nextRemarkIn := time.Duration(2+rand.Intn(4)) * time.Second
	lastRemarkTime := time.Time{}

	ticker := time.NewTicker(s.tickDuration)
	defer ticker.Stop()

	if s.loopStarted != nil {
		close(s.loopStarted)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pendingMu.Lock()
			update := s.pendingUpdate
			s.pendingUpdate = ""
			s.pendingMu.Unlock()

			if update != "" {
				s.sendData("\n")
				s.sendInline(update)
				s.sendData(" ")
				lastRemarkTime = time.Now()
				nextRemarkIn = time.Duration(5+rand.Intn(5)) * time.Second
			} else if time.Since(lastRemarkTime) >= nextRemarkIn {
				remark := remarks[ri%len(remarks)]
				ri++
				s.sendData("\n")
				s.sendInline(remark)
				s.sendData(" ")
				lastRemarkTime = time.Now()
				nextRemarkIn = time.Duration(5+rand.Intn(5)) * time.Second
			} else {
				s.sendData(".")
			}
		}
	}
}

func (s *loadingWriter) waitForCompletion(timeout time.Duration) bool {
	if s.done == nil {
		return true
	}
	select {
	case <-s.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (s *loadingWriter) sendInline(text string) {
	chunkSize := 10
	if s.charPerSecond > 0 {
		chunkSize = max(3, int(s.charPerSecond)/15)
	}

	runes := []rune(text)
	for i := 0; i < len(runes); {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunk := string(runes[i:end])
		s.sendData(chunk)
		i = end

		if i < len(runes) && s.charPerSecond > 0 {
			time.Sleep(time.Duration(float64(time.Second) * float64(len(chunk)) / s.charPerSecond))
		}
	}
}

func (s *loadingWriter) sendLine(line string) {
	if line == "" {
		s.sendData("\n")
		return
	}
	s.sendInline(line)
	s.sendData("\n")
}

func (s *loadingWriter) sendData(data string) {
	type Delta struct {
		ReasoningContent string `json:"reasoning_content"`
	}
	type Choice struct {
		Delta Delta `json:"delta"`
	}
	type SSEMessage struct {
		Choices []Choice `json:"choices"`
	}

	msg := SSEMessage{
		Choices: []Choice{
			{
				Delta: Delta{
					ReasoningContent: data,
				},
			},
		},
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		s.logger.Errorf("<%s> Failed to marshal SSE message: %v", s.modelName, err)
		return
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	// Once ServeHTTP has reclaimed the writer (release), writing/flushing it
	// races the real handler or panics on a finalized response. Stop here.
	if s.released {
		return
	}

	if _, err = fmt.Fprintf(s.writer, "data: %s\n\n", jsonData); err != nil {
		s.logger.Debugf("<%s> Failed to write SSE data (client likely disconnected): %v", s.modelName, err)
		return
	}
	if flusher, ok := s.writer.(http.Flusher); ok {
		flusher.Flush()
	}
}

// release fences the loadingWriter off from the underlying ResponseWriter.
// After it returns, the streaming goroutine will not write to or flush the
// writer again: any in-flight write completes under writeMu first, and later
// writes short-circuit on released. The caller can then safely hand the writer
// to the real handler or let ServeHTTP return without racing a finalized
// response (a use-after-return Flush panics on the recycled *bufio.Writer).
func (s *loadingWriter) release() {
	s.writeMu.Lock()
	s.released = true
	s.writeMu.Unlock()
}

func (s *loadingWriter) Header() http.Header {
	return s.writer.Header()
}

func (s *loadingWriter) Write(data []byte) (int, error) {
	return s.writer.Write(data)
}

func (s *loadingWriter) WriteHeader(statusCode int) {
	if s.hasWritten {
		return
	}
	s.hasWritten = true
	s.writer.WriteHeader(statusCode)
	s.Flush()
}

func (s *loadingWriter) Flush() {
	if flusher, ok := s.writer.(http.Flusher); ok {
		flusher.Flush()
	}
}
