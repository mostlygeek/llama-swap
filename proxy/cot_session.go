package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

type cotEntry struct {
	content       string
	ensureNewline bool
}

type cotSession struct {
	mu           sync.Mutex
	lines        chan cotEntry
	buffer       []cotEntry
	attached     bool
	finished     bool
	aborted      bool
	pendingBlank bool
	inlineBreak  bool
	writer       http.ResponseWriter
	flusher      http.Flusher
	writerReady  bool
}

func newCOTSession() *cotSession {
	return &cotSession{
		lines: make(chan cotEntry, 32),
	}
}

func (s *cotSession) appendLine(line string) {
	if s.consumeInlineBreak() {
		s.appendEntry(cotEntry{content: "", ensureNewline: true})
	}
	s.appendEntry(cotEntry{content: line, ensureNewline: true})
}

func (s *cotSession) appendInline(content string) {
	if content == "" {
		return
	}
	s.markInlineBreakPending()
	s.appendEntry(cotEntry{content: content, ensureNewline: false})
}

func (s *cotSession) appendEntry(entry cotEntry) {
	s.mu.Lock()
	if s.aborted || s.finished {
		// Session closed before attachment; drop new lines.
		s.mu.Unlock()
		return
	}

	if s.writerReady {
		writer := s.writer
		flusher := s.flusher
		s.mu.Unlock()
		emitToWriter(writer, flusher, entry)
		return
	}

	if s.attached {
		ch := s.lines
		s.mu.Unlock()
		ch <- entry
		return
	}

	s.buffer = append(s.buffer, entry)
	s.mu.Unlock()
}

func (s *cotSession) attach(body io.ReadCloser) io.ReadCloser {
	s.mu.Lock()
	if s.aborted {
		s.mu.Unlock()
		return body
	}
	if s.attached {
		s.mu.Unlock()
		return body
	}
	s.attached = true
	buffer := append([]cotEntry(nil), s.buffer...)
	pendingBlank := s.pendingBlank
	writerReady := s.writerReady
	lines := s.lines
	s.pendingBlank = false
	s.buffer = nil
	s.mu.Unlock()

	if writerReady {
		if pendingBlank {
			emitToWriter(s.writer, s.flusher, cotEntry{content: "", ensureNewline: true})
		}
		return body
	}

	stream := newCoTStream(lines, body)
	go func() {
		defer func() {
			_ = recover()
		}()
		for _, entry := range buffer {
			lines <- entry
		}
		if pendingBlank {
			defer func() { _ = recover() }()
			lines <- cotEntry{content: "", ensureNewline: true}
			close(lines)
		}
	}()

	return stream
}

func (s *cotSession) finish() {
	s.mu.Lock()
	if s.aborted || s.finished {
		s.mu.Unlock()
		return
	}
	s.finished = true
	s.inlineBreak = false
	if s.writerReady {
		writer := s.writer
		flusher := s.flusher
		s.mu.Unlock()
		emitToWriter(writer, flusher, cotEntry{content: "", ensureNewline: true})
		return
	}
	if s.attached {
		lines := s.lines
		s.mu.Unlock()
		func() {
			defer func() { _ = recover() }()
			lines <- cotEntry{content: "", ensureNewline: true}
			close(lines)
		}()
		return
	}
	s.pendingBlank = true
	s.mu.Unlock()
}

func (s *cotSession) abort() {
	s.mu.Lock()
	if s.aborted {
		s.mu.Unlock()
		return
	}
	s.aborted = true
	s.inlineBreak = false
	if s.attached {
		close(s.lines)
	}
	s.buffer = nil
	s.writer = nil
	s.flusher = nil
	s.mu.Unlock()
}

func (s *cotSession) attachWriter(w http.ResponseWriter) bool {
	if w == nil {
		return false
	}

	flusher, _ := w.(http.Flusher)

	s.mu.Lock()
	if s.aborted || s.writerReady {
		s.mu.Unlock()
		return false
	}
	s.writerReady = true
	s.writer = w
	s.flusher = flusher
	buffer := append([]cotEntry(nil), s.buffer...)
	pendingBlank := s.pendingBlank
	finished := s.finished
	s.buffer = nil
	s.pendingBlank = false
	s.mu.Unlock()

	for _, entry := range buffer {
		emitToWriter(w, flusher, entry)
	}

	if pendingBlank || finished {
		emitToWriter(w, flusher, cotEntry{content: "", ensureNewline: true})
	}

	return true
}

func (s *cotSession) consumeInlineBreak() bool {
	s.mu.Lock()
	if s.inlineBreak && !s.aborted && !s.finished {
		s.inlineBreak = false
		s.mu.Unlock()
		return true
	}
	s.mu.Unlock()
	return false
}

func (s *cotSession) markInlineBreakPending() {
	s.mu.Lock()
	if !s.aborted && !s.finished {
		s.inlineBreak = true
	}
	s.mu.Unlock()
}

type cotStream struct {
	reader *io.PipeReader
	done   chan struct{}
}

func newCoTStream(lines <-chan cotEntry, body io.ReadCloser) io.ReadCloser {
	pipeReader, pipeWriter := io.Pipe()
	stream := &cotStream{
		reader: pipeReader,
		done:   make(chan struct{}),
	}

	go stream.run(lines, body, pipeWriter)
	return stream
}

func (cs *cotStream) run(lines <-chan cotEntry, body io.ReadCloser, writer *io.PipeWriter) {
	defer close(cs.done)
	defer body.Close()

	for entry := range lines {
		payload := formatCoTEntry(entry)
		if len(payload) == 0 {
			continue
		}
		if _, err := writer.Write(payload); err != nil {
			writer.CloseWithError(err)
			return
		}
	}
	if _, err := io.Copy(writer, body); err != nil {
		writer.CloseWithError(err)
		return
	}
	writer.Close()
}

func (cs *cotStream) Read(p []byte) (int, error) {
	return cs.reader.Read(p)
}

func (cs *cotStream) Close() error {
	err := cs.reader.Close()
	<-cs.done
	return err
}

func formatCoTEntry(entry cotEntry) []byte {
	content := entry.content
	if entry.ensureNewline {
		if content == "" {
			content = "\n"
		} else if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
	}

	encoded, err := json.Marshal(content)
	if err != nil {
		return nil
	}
	return []byte(fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"reasoning_content\":%s}}]}\n\n", string(encoded)))
}

func emitToWriter(w http.ResponseWriter, flusher http.Flusher, entry cotEntry) {
	if w == nil {
		return
	}
	payload := formatCoTEntry(entry)
	if len(payload) == 0 {
		return
	}
	if _, err := w.Write(payload); err != nil {
		return
	}
	if flusher != nil {
		flusher.Flush()
	}
}
