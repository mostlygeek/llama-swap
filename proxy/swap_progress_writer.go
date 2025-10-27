package proxy

import (
	"bytes"
	"strings"
	"sync"
)

type swapProgressWriter struct {
	mu            sync.Mutex
	buffer        []byte
	lineHandler   func(string)
	inlineHandler func(string)
}

func newSwapProgressWriter(lineHandler func(string), inlineHandler func(string)) *swapProgressWriter {
	if lineHandler == nil {
		lineHandler = func(string) {}
	}
	if inlineHandler == nil {
		inlineHandler = func(string) {}
	}
	return &swapProgressWriter{lineHandler: lineHandler, inlineHandler: inlineHandler}
}

func (w *swapProgressWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.buffer = append(w.buffer, p...)
	for {
		idx := bytes.IndexByte(w.buffer, '\n')
		if idx != -1 {
			raw := string(w.buffer[:idx])
			w.buffer = w.buffer[idx+1:]

			line := strings.TrimRight(raw, "\r")
			if len(strings.TrimSpace(line)) != 0 {
				if isDotLine(line) {
					w.emitDots(line)
				} else {
					w.emitLine(line)
				}
			}
			continue
		}

		if w.emitDotsFromBuffer() {
			continue
		}

		break
	}

	return len(p), nil
}

func (w *swapProgressWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.buffer) == 0 {
		return
	}
	if w.emitDotsFromBuffer() {
		return
	}

	line := strings.TrimRight(string(w.buffer), "\r")
	w.buffer = nil
	if len(strings.TrimSpace(line)) != 0 {
		if isDotLine(line) {
			w.emitDots(line)
		} else {
			w.emitLine(line)
		}
	}
}

func (w *swapProgressWriter) emitLine(line string) {
	if strings.Contains(line, "load_tensors:") {
		w.lineHandler(line)
	}
}

func (w *swapProgressWriter) emitDots(line string) {
	for _, r := range line {
		if r == '.' {
			w.inlineHandler(".")
		}
	}
}

func (w *swapProgressWriter) emitDotsFromBuffer() bool {
	if len(w.buffer) == 0 {
		return false
	}

	for _, b := range w.buffer {
		if b == '.' || b == '\r' {
			continue
		}
		return false
	}

	for len(w.buffer) > 0 {
		b := w.buffer[0]
		w.buffer = w.buffer[1:]
		if b == '.' {
			w.inlineHandler(".")
		}
	}
	return true
}

func isDotLine(line string) bool {
	if line == "" {
		return false
	}
	for _, r := range line {
		if r != '.' {
			return false
		}
	}
	return true
}
