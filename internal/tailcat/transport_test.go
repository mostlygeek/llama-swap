package tailcat

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tailcatlib "github.com/tailscale/tailcat"
	"tailscale.com/derp/derpserver"
	"tailscale.com/net/stun"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

func TestTailcatTransport_ChannelListenerClose(t *testing.T) {
	listener := newChannelListener()
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	delivered := make(chan bool, 1)
	go func() { delivered <- listener.deliver(left) }()
	accepted, err := listener.Accept()
	if err != nil || accepted != left || !<-delivered {
		t.Fatalf("Accept = %v, %v", accepted, err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := listener.Accept(); err == nil {
		t.Fatal("Accept succeeded after Close")
	}
}

func TestTailcatTransport_ReadHeaderTimeout(t *testing.T) {
	listener := newChannelListener()
	server := newHTTPServer(ServerOptions{
		Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	})
	go server.Serve(listener)
	t.Cleanup(func() {
		server.Close()
		listener.Close()
	})

	client, connection := net.Pipe()
	defer client.Close()
	if !listener.deliver(connection) {
		t.Fatal("could not deliver connection to HTTP server")
	}
	if _, err := client.Write([]byte("GET / HTTP/1.1\r\nHost: example.test\r\n")); err != nil {
		t.Fatalf("write incomplete request headers: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(2 * serverReadHeaderTimeout)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err := io.ReadAll(client)
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
			Region:            []*tailcfg.DERPRegion{region},
		},
	}}
	server, err := Start(t.Context(), ServerOptions{
		PrivateKey:     serverKey,
		AllowedClients: []string{clientPrivate.Public().String()},
		Logger:         testLogger{t},
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://server.tailcat/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP request over Tailcat: %v", err)
	}
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
