package process

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var simpleResponderPath string

func skipIfNoSimpleResponder(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(simpleResponderPath); os.IsNotExist(err) {
		t.Skipf("simple-responder not found at %s, run `make simple-responder`", simpleResponderPath)
	}
}

func TestMain(m *testing.M) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	if goos == "windows" {
		simpleResponderPath = filepath.Join("..", "..", "build", "simple-responder.exe")
	} else {
		simpleResponderPath = filepath.Join("..", "..", "build", fmt.Sprintf("simple-responder_%s_%s", goos, goarch))
	}
	m.Run()
}

func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("getFreePort: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func simpleResponderCmd(t *testing.T, args ...string) (string, int) {
	port := getFreePort(t)
	cmdPath := filepath.ToSlash(simpleResponderPath)
	base := []string{cmdPath, fmt.Sprintf("-port %d", port)}
	base = append(base, args...)
	return strings.Join(base, " "), port
}
