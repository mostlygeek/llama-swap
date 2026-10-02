package swaputil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unsafe"
)

type countingReader struct {
	r     io.Reader
	reads int
}

func (c *countingReader) Read(p []byte) (int, error) {
	c.reads++
	return c.r.Read(p)
}

func TestBufferRequestBody_ReadsSourceOnce(t *testing.T) {
	src := &countingReader{r: strings.NewReader(`{"model":"m"}`)}
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", src)
	r.Header.Set("Content-Type", "application/json")

	r, err := BufferRequestBody(httptest.NewRecorder(), r)
	if err != nil {
		t.Fatal(err)
	}
	readsAfterBuffer := src.reads

	for range 3 {
		if _, err := ExtractModel(r); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := RequestBody(r)
	second, _ := RequestBody(r)

	if src.reads != readsAfterBuffer {
		t.Fatalf("source read %d more times after buffering", src.reads-readsAfterBuffer)
	}
	if unsafe.SliceData(first) != unsafe.SliceData(second) {
		t.Fatal("RequestBody copied the buffered bytes")
	}
	if got, _ := io.ReadAll(r.Body); string(got) != `{"model":"m"}` {
		t.Fatalf("r.Body = %q", got)
	}
}

func TestBufferRequestBody_Idempotent(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("x"))
	first, _ := BufferRequestBody(httptest.NewRecorder(), r)
	second, _ := BufferRequestBody(httptest.NewRecorder(), first)
	if first != second {
		t.Fatal("second call should return the same request")
	}
}

func TestReplaceRequestBody_UpdatesBuffer(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"model":"a"}`))
	r, _ = BufferRequestBody(httptest.NewRecorder(), r)

	ReplaceRequestBody(r, []byte(`{"model":"b"}`))

	got, _ := RequestBody(r)
	if string(got) != `{"model":"b"}` {
		t.Fatalf("RequestBody = %q, want replaced body", got)
	}
	if r.ContentLength != int64(len(got)) || r.Header.Get("Content-Length") != "13" {
		t.Fatalf("length headers not updated: %d %q", r.ContentLength, r.Header.Get("Content-Length"))
	}
}
