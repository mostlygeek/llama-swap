package server

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// parseResponseObjects normalizes a response body into the JSON objects metric
// extractors inspect. SSE responses yield one object per data line; plain JSON
// yields one object; array responses (e.g. /infill) yield one object per
// element so the last element's timings win.
func parseResponseObjects(contentType string, body []byte) ([]gjson.Result, error) {
	if strings.Contains(contentType, "text/event-stream") {
		return parseEventStream(body)
	}
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("invalid JSON response body")
	}
	parsed := gjson.ParseBytes(body)
	if parsed.IsArray() {
		return parsed.Array(), nil
	}
	return []gjson.Result{parsed}, nil
}

// parseEventStream returns the JSON objects carried by SSE data lines.
func parseEventStream(body []byte) ([]gjson.Result, error) {
	var objects []gjson.Result
	prefix := []byte("data:")

	for offset := 0; offset < len(body); {
		nl := bytes.IndexByte(body[offset:], '\n')
		var line []byte
		if nl == -1 {
			line = body[offset:]
			offset = len(body)
		} else {
			line = body[offset : offset+nl]
			offset += nl + 1
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 || !bytes.HasPrefix(line, prefix) {
			continue
		}
		data := bytes.TrimSpace(line[len(prefix):])
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) || !gjson.ValidBytes(data) {
			continue
		}
		objects = append(objects, gjson.ParseBytes(data))
	}

	if len(objects) == 0 {
		return nil, fmt.Errorf("no valid JSON data found in stream")
	}
	return objects, nil
}
