package tailcat

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	tailcatlib "github.com/tailscale/tailcat"
	"tailscale.com/derp/derpserver"
	"tailscale.com/net/stun"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

type singleConnListener struct {
	conn net.Conn
	addr net.Addr
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	if l.conn == nil {
		return nil, net.ErrClosed
	}
	conn := l.conn
	l.conn = nil
	return conn, nil
}

func (l *singleConnListener) Close() error   { return nil }
func (l *singleConnListener) Addr() net.Addr { return l.addr }

func TestTailcatTransport_UnrecognizedNodeKeyReturnsForbidden(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	var handled atomic.Bool
	server := newHTTPServer(ServerOptions{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		handled.Store(true)
	})})
	listener := &authenticatedListener{
		Listener: &singleConnListener{conn: serverConn, addr: serverConn.LocalAddr()},
		server:   &tailcatlib.Server{},
		runtime:  &Server{},
	}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })

	if err := clientConn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(clientConn, "GET /health HTTP/1.1\r\nHost: server.tailcat\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(clientConn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("read response body: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if handled.Load() {
		t.Fatal("unrecognized peer reached application handler")
	}
}

func TestTailcatTransport_ReadHeaderTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := newHTTPServer(ServerOptions{
		Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	})
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })

	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("GET / HTTP/1.1\r\nHost: example.test\r\n")); err != nil {
		t.Fatalf("write incomplete request headers: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(2 * serverReadHeaderTimeout)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = io.ReadAll(client)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read after incomplete headers: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*serverReadHeaderTimeout {
		t.Fatalf("incomplete headers remained open for %v, want no more than %v", elapsed, 2*serverReadHeaderTimeout)
	}
}

func TestTailcatTransport_ProcessLifetimePeerIdentity(t *testing.T) {
	first := NewClient("peer-test", "invalid-until-dial", nil, nil)
	second := NewClient("peer-test", "invalid-until-dial", nil, nil)
	other := NewClient("peer-other", "invalid-until-dial", nil, nil)
	if first.PublicKey() != second.PublicKey() {
		t.Fatal("same peer did not retain its process-lifetime identity")
	}
	if first.PublicKey() == other.PublicKey() {
		t.Fatal("different peers unexpectedly share an identity")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = first.Close(ctx)
	_ = second.Close(ctx)
	_ = other.Close(ctx)
}

func TestTailcatTransport_LocalDERPHTTP(t *testing.T) {
	region := runLocalDERP(t)

	clientPrivate := key.NewNode()
	gotSource := make(chan string, 1)
	serverPrivate := key.NewNode()
	serverKey := &PrivateKey{value: tailcatlib.PrivateKey{
		Private: serverPrivate,
		Public: tailcatlib.ConnInfo{
			ServerPublic:      tailcatlib.NodePublic{NodePublic: serverPrivate.Public()},
			ServerDiscoPublic: tailcatlib.DiscoPublicForNode(serverPrivate),
			PresharedKey:      tailcatlib.NewPresharedKey(),
			Region:            []*tailcfg.DERPRegion{region},
		},
	}}
	server, err := Start(t.Context(), ServerOptions{
		PrivateKey: serverKey,
		Logger:     testLogger{t},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			source, ok := SourceFromContext(r.Context())
			if !ok {
				http.Error(w, "missing authenticated source", http.StatusInternalServerError)
				return
			}
			gotSource <- source
			io.WriteString(w, "tailcat over HTTP")
		}),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !server.tailcat.PresharedKey.Equal(serverKey.value.Public.PresharedKey) {
		t.Fatal("server did not retain the persistent key's pre-shared key")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Close(ctx); err != nil {
			t.Errorf("server Close: %v", err)
		}
	})

	clientKey := &PrivateKey{value: tailcatlib.PrivateKey{Private: clientPrivate}}
	client := NewClient("local-derp", server.Address(), clientKey, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Close(ctx); err != nil {
			t.Errorf("client Close: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	// The first packet can race the initial DERP connection. Tailcat's own
	// integration helper waits on internal health state; the adapter stays on
	// the public API and retries the authenticated handshake instead.
	for {
		pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := client.client.Ping(pingCtx)
		pingCancel()
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("client Ping: %v", ctx.Err())
		}
	}
	transport := &http.Transport{DialContext: client.DialContext}
	httpClient := &http.Client{Transport: transport}
	var resp *http.Response
	var responseCancel context.CancelFunc
	for {
		// A successful Ping means the peers authenticated, but the first TCP
		// flow can still race the local DERP connection coming fully online.
		// Keep each attempt short so slow CI runners can retry within the
		// test's overall deadline.
		requestCtx, requestCancel := context.WithTimeout(ctx, 2*time.Second)
		req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://server.tailcat/health", nil)
		if err == nil {
			resp, err = httpClient.Do(req)
		}
		if err == nil {
			responseCancel = requestCancel
			break
		}
		requestCancel()
		httpClient.CloseIdleConnections()
		if ctx.Err() != nil {
			t.Fatalf("HTTP request over Tailcat: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("HTTP request over Tailcat: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer responseCancel()
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "tailcat over HTTP"; got != want {
		t.Fatalf("response body = %q, want %q", got, want)
	}
	if got, want := <-gotSource, "tc:"+clientPrivate.Public().String(); got != want {
		t.Fatalf("source = %q, want %q", got, want)
	}

	// Leave the HTTP keep-alive connection idle so Shutdown makes the server
	// the active TCP closer. Tailcat then retains a TIME-WAIT endpoint, which
	// must not consume llama-swap's full graceful-shutdown budget.
	closeCtx, closeCancel := context.WithTimeout(t.Context(), 5*time.Second)
	started := time.Now()
	err = server.Close(closeCtx)
	closeCancel()
	if err != nil {
		t.Fatalf("server Close: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*serverDrainTimeout {
		t.Fatalf("server Close took %v, want no more than %v", elapsed, 2*serverDrainTimeout)
	}
	transport.CloseIdleConnections()
}

func TestTailcatTransport_ReconnectAfterServerRestart(t *testing.T) {
	region := runLocalDERP(t)
	private := key.NewNode()
	serverKey := &PrivateKey{value: tailcatlib.PrivateKey{
		Private: private,
		Public: tailcatlib.ConnInfo{
			ServerPublic:      tailcatlib.NodePublic{NodePublic: private.Public()},
			ServerDiscoPublic: tailcatlib.DiscoPublicForNode(private),
			PresharedKey:      tailcatlib.NewPresharedKey(),
			Region:            []*tailcfg.DERPRegion{region},
		},
	}}
	start := func() (*Server, <-chan string) {
		seenKeys := make(chan string, 16)
		s, err := Start(t.Context(), ServerOptions{
			PrivateKey: serverKey,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if nodeKey, ok := NodeKeyFromContext(r.Context()); ok {
					seenKeys <- nodeKey
				}
				io.WriteString(w, "ok")
			}),
		})
		if err != nil {
			t.Fatalf("start Tailcat server: %v", err)
		}
		return s, seenKeys
	}
	server, firstKeys := start()
	client := NewClient("restart-test", server.Address(), nil, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Close(ctx)
		_ = server.Close(ctx)
	})
	httpClient := &http.Client{Transport: &http.Transport{
		DialContext:       client.DialContext,
		DisableKeepAlives: true,
	}}
	get := func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://server.tailcat/health", nil)
		if err != nil {
			return err
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("status = %d", resp.StatusCode)
		}
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	initialCtx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	for {
		requestCtx, requestCancel := context.WithTimeout(initialCtx, 3*time.Second)
		err := get(requestCtx)
		requestCancel()
		if err == nil {
			break
		}
		if initialCtx.Err() != nil {
			t.Fatalf("initial request: %v", err)
		}
	}
	if nodeKey := <-firstKeys; nodeKey != client.PublicKey() {
		t.Fatalf("initial node key = %q, want %q", nodeKey, client.PublicKey())
	}
	ctx, closeCancel := context.WithTimeout(t.Context(), 5*time.Second)
	if err := server.Close(ctx); err != nil {
		t.Fatalf("close first server: %v", err)
	}
	closeCancel()
	server, restartedKeys := start()

	reconnectCtx, reconnectCancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer reconnectCancel()
	if err := get(reconnectCtx); err != nil {
		t.Fatalf("request after server restart: %v", err)
	}
	if nodeKey := <-restartedKeys; nodeKey != client.PublicKey() {
		t.Fatalf("node key changed across restart: %q", nodeKey)
	}
}

type testLogger struct{ t *testing.T }

func (l testLogger) Debugf(format string, args ...any) {}
func (l testLogger) Warnf(format string, args ...any)  { l.t.Logf(format, args...) }

func runLocalDERP(t *testing.T) *tailcfg.DERPRegion {
	t.Helper()
	derp := derpserver.New(key.NewNode(), func(string, ...any) {})
	server := httptest.NewUnstartedServer(derpserver.Handler(derp))
	server.Config.TLSNextProto = make(map[string]func(*http.Server, *tls.Conn, http.Handler))
	server.StartTLS()

	stunConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen for local STUN: %v", err)
	}
	go func() {
		var packet [1500]byte
		for {
			n, source, err := stunConn.ReadFromUDPAddrPort(packet[:])
			if err != nil {
				return
			}
			transaction, err := stun.ParseBindingRequest(packet[:n])
			if err != nil {
				continue
			}
			_, _ = stunConn.WriteToUDPAddrPort(stun.Response(transaction, source), source)
		}
	}()
	t.Cleanup(func() {
		stunConn.Close()
		server.CloseClientConnections()
		server.Close()
		derp.Close()
	})

	return &tailcfg.DERPRegion{
		RegionID:   1,
		RegionCode: "test",
		Nodes: []*tailcfg.DERPNode{{
			Name:             "local",
			RegionID:         1,
			HostName:         "127.0.0.1",
			IPv4:             "127.0.0.1",
			IPv6:             "none",
			STUNPort:         stunConn.LocalAddr().(*net.UDPAddr).Port,
			DERPPort:         server.Listener.Addr().(*net.TCPAddr).Port,
			InsecureForTests: true,
		}},
	}
}
