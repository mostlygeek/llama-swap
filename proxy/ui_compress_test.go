package proxy

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestServeCompressedFile_Brotli(t *testing.T) {
	// Create test content
	content := []byte("This is test content that should be compressed with brotli")
	brContent := []byte("fake-brotli-compressed-data")

	// Create a test filesystem
	mapFS := fstest.MapFS{
		"test.js":    {Data: content, ModTime: time.Now()},
		"test.js.br": {Data: brContent, ModTime: time.Now()},
		"test.js.gz": {Data: []byte("fake-gzip-data"), ModTime: time.Now()},
	}
	fs := http.FS(mapFS)

	req := httptest.NewRequest(http.MethodGet, "/test.js", nil)
	req.Header.Set("Accept-Encoding", "br, gzip")
	w := httptest.NewRecorder()

	ServeCompressedFile(fs, w, req, "test.js")

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Check that brotli is used (preferred over gzip)
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "br" {
		t.Errorf("Expected Content-Encoding 'br', got '%s'", encoding)
	}

	if vary := resp.Header.Get("Vary"); vary != "Accept-Encoding" {
		t.Errorf("Expected Vary 'Accept-Encoding', got '%s'", vary)
	}

	if !bytes.Equal(body, brContent) {
		t.Errorf("Expected brotli content, got %s", string(body))
	}
}

func TestServeCompressedFile_Gzip(t *testing.T) {
	// Create test content
	content := []byte("This is test content that should be compressed with gzip")
	gzContent := []byte("fake-gzip-compressed-data")

	// Create a test filesystem without brotli
	mapFS := fstest.MapFS{
		"test.js":    {Data: content, ModTime: time.Now()},
		"test.js.gz": {Data: gzContent, ModTime: time.Now()},
	}
	fs := http.FS(mapFS)

	req := httptest.NewRequest(http.MethodGet, "/test.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	ServeCompressedFile(fs, w, req, "test.js")

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	if encoding := resp.Header.Get("Content-Encoding"); encoding != "gzip" {
		t.Errorf("Expected Content-Encoding 'gzip', got '%s'", encoding)
	}

	if !bytes.Equal(body, gzContent) {
		t.Errorf("Expected gzip content, got %s", string(body))
	}
}

func TestServeCompressedFile_UncompressedFallback(t *testing.T) {
	// Create test content
	content := []byte("This is uncompressed test content")

	// Create a test filesystem without compressed versions
	mapFS := fstest.MapFS{
		"test.js": {Data: content, ModTime: time.Now()},
	}
	fs := http.FS(mapFS)

	req := httptest.NewRequest(http.MethodGet, "/test.js", nil)
	req.Header.Set("Accept-Encoding", "br, gzip")
	w := httptest.NewRecorder()

	ServeCompressedFile(fs, w, req, "test.js")

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Should not have Content-Encoding header since we're serving uncompressed
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" {
		t.Errorf("Expected no Content-Encoding, got '%s'", encoding)
	}

	if !bytes.Equal(body, content) {
		t.Errorf("Expected original content, got %s", string(body))
	}
}

func TestServeCompressedFile_NoAcceptEncoding(t *testing.T) {
	// Create test content
	content := []byte("This is test content")

	// Create a test filesystem with compressed versions
	mapFS := fstest.MapFS{
		"test.js":    {Data: content, ModTime: time.Now()},
		"test.js.br": {Data: []byte("brotli"), ModTime: time.Now()},
		"test.js.gz": {Data: []byte("gzip"), ModTime: time.Now()},
	}
	fs := http.FS(mapFS)

	req := httptest.NewRequest(http.MethodGet, "/test.js", nil)
	// No Accept-Encoding header
	w := httptest.NewRecorder()

	ServeCompressedFile(fs, w, req, "test.js")

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Should serve uncompressed content
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" {
		t.Errorf("Expected no Content-Encoding, got '%s'", encoding)
	}

	if !bytes.Equal(body, content) {
		t.Errorf("Expected original content, got %s", string(body))
	}
}

func TestServeCompressedFile_NotFound(t *testing.T) {
	mapFS := fstest.MapFS{}
	fs := http.FS(mapFS)

	req := httptest.NewRequest(http.MethodGet, "/nonexistent.js", nil)
	w := httptest.NewRecorder()

	ServeCompressedFile(fs, w, req, "nonexistent.js")

	resp := w.Result()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", resp.StatusCode)
	}
}

func TestSelectEncoding(t *testing.T) {
	tests := []struct {
		acceptEncoding string
		wantEncoding   string
		wantExt        string
	}{
		{"br, gzip", "br", ".br"},
		{"gzip, deflate", "gzip", ".gz"},
		{"gzip", "gzip", ".gz"},
		{"br", "br", ".br"},
		{"", "", ""},
		{"deflate", "", ""},
		{"br;q=1.0, gzip;q=0.5", "br", ".br"},
		{"gzip;q=1.0, br;q=0.5", "br", ".br"},
		{"browser", "", ""},
		{"compress, deflate", "", ""},
	}

	for _, tt := range tests {
		gotEncoding, gotExt := selectEncoding(tt.acceptEncoding)
		if gotEncoding != tt.wantEncoding || gotExt != tt.wantExt {
			t.Errorf("selectEncoding(%q) = (%q, %q), want (%q, %q)",
				tt.acceptEncoding, gotEncoding, gotExt, tt.wantEncoding, tt.wantExt)
		}
	}
}

// Test with actual pre-compressed files from ui_dist
func TestServeCompressedFile_RealFiles(t *testing.T) {
	// Check if ui_dist exists
	if _, err := os.Stat("./ui_dist"); os.IsNotExist(err) {
		t.Skip("ui_dist not found, skipping real file test")
	}

	// Find a .js or .css file that has compressed versions
	entries, err := os.ReadDir("./ui_dist/assets")
	if err != nil {
		t.Skipf("Could not read ui_dist/assets: %v", err)
	}

	var testFile string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".js") && !strings.HasSuffix(name, ".js.gz") && !strings.HasSuffix(name, ".js.br") {
			// Check if compressed versions exist
			base := strings.TrimSuffix(name, ".js")
			if _, err := os.Stat(filepath.Join("./ui_dist/assets", base+".js.gz")); err == nil {
				testFile = "assets/" + name
				break
			}
		}
	}

	if testFile == "" {
		t.Skip("No suitable test file found with compressed versions")
	}

	fs := http.FS(os.DirFS("./ui_dist"))

	// Test brotli
	t.Run("brotli", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/"+testFile, nil)
		req.Header.Set("Accept-Encoding", "br")
		w := httptest.NewRecorder()

		ServeCompressedFile(fs, w, req, testFile)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected status 200, got %d", resp.StatusCode)
		}

		if encoding := resp.Header.Get("Content-Encoding"); encoding != "br" {
			t.Errorf("Expected Content-Encoding 'br', got '%s'", encoding)
		}
	})

	// Test gzip
	t.Run("gzip", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/"+testFile, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		w := httptest.NewRecorder()

		ServeCompressedFile(fs, w, req, testFile)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected status 200, got %d", resp.StatusCode)
		}

		if encoding := resp.Header.Get("Content-Encoding"); encoding != "gzip" {
			t.Errorf("Expected Content-Encoding 'gzip', got '%s'", encoding)
		}

		// Verify it's valid gzip
		reader, err := gzip.NewReader(resp.Body)
		if err != nil {
			t.Errorf("Expected valid gzip content: %v", err)
			return
		}
		defer reader.Close()

		// Just read to verify it's valid
		_, err = io.Copy(io.Discard, reader)
		if err != nil {
			t.Errorf("Failed to decompress gzip: %v", err)
		}
	})
}
