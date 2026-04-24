package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var loadingPageHTML string

var (
	flagMac            = flag.String("mac", "", "mac address to send WoL packet to")
	flagUpstream       = flag.String("upstream", "", "upstream proxy address to send requests to")
	flagListen         = flag.String("listen", ":8080", "listen address to listen on")
	flagLog            = flag.String("log", "info", "log level (debug, info, warn, error)")
	flagTimeout        = flag.Int("timeout", 60, "seconds requests wait for upstream response before failing")
	flagUpstreamAPIKey = flag.String("upstream-api-key", "", "optional API key injected into proxied upstream requests")
	flagSourceIP       = flag.String("source-ip", "", "source IPv4 address for WoL packets (binds to specific interface)")
)

func main() {
	flag.Parse()

	switch *flagLog {
	case "debug":
		slog.SetLogLoggerLevel(slog.LevelDebug)
	case "info":
		slog.SetLogLoggerLevel(slog.LevelInfo)
	case "warn":
		slog.SetLogLoggerLevel(slog.LevelWarn)
	case "error":
		slog.SetLogLoggerLevel(slog.LevelError)
	default:
		slog.Error("invalid log level", "logLevel", *flagLog)
		return
	}

	if *flagListen == "" {
		slog.Error("listen address is required")
		return
	}

	if *flagMac == "" {
		slog.Error("mac address is required")
		return
	}

	if *flagTimeout < 1 {
		slog.Error("timeout must be greater than 0")
		return
	}

	var upstreamURL *url.URL
	var err error
	if _, err = net.ParseMAC(*flagMac); err != nil {
		slog.Error("invalid mac address", "error", err)
		return
	}

	if *flagSourceIP != "" {
		ip := net.ParseIP(*flagSourceIP)
		if ip == nil || ip.To4() == nil {
			slog.Error("invalid source IP, must be a valid IPv4 address", "source-ip", *flagSourceIP)
			return
		}
	}

	if *flagUpstream == "" {
		slog.Error("upstream proxy address is required")
		return
	} else {
		upstreamURL, err = url.ParseRequestURI(*flagUpstream)
		if err != nil {
			slog.Error("error parsing upstream url", "error", err)
			return
		}
	}

	maskedKey := "(not set)"
	if *flagUpstreamAPIKey != "" {
		key := *flagUpstreamAPIKey
		if len(key) > 2 {
			maskedKey = strings.Repeat("*", len(key)-2) + key[len(key)-2:]
		} else {
			maskedKey = strings.Repeat("*", len(key))
		}
	}
	slog.Info("starting wol-proxy",
		"listen", *flagListen,
		"upstream", *flagUpstream,
		"mac", *flagMac,
		"source-ip", *flagSourceIP,
		"timeout", *flagTimeout,
		"upstream-api-key", maskedKey,
	)

	proxy := newProxy(upstreamURL)
	server := &http.Server{
		Addr:    *flagListen,
		Handler: proxy,
	}

	go func() {
		slog.Info("server starting on", "address", *flagListen)
		if err := server.ListenAndServe(); err != nil {
			slog.Error("error starting server", "error", err)
		}
	}()

	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt)
	<-ctx.Done()
	server.Close()
}

type upstreamStatus string

const (
	notready upstreamStatus = "not ready"
	ready    upstreamStatus = "ready"
)

type proxyServer struct {
	upstreamProxy  *httputil.ReverseProxy
	upstreamURL    *url.URL
	upstreamAPIKey string
	failCount      int
	statusMutex    sync.RWMutex
	status         upstreamStatus
	sseCancel      context.CancelFunc
}

type proxyResponseWriter struct {
	http.ResponseWriter
	written bool
}

func (w *proxyResponseWriter) WriteHeader(code int) {
	w.written = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *proxyResponseWriter) Write(b []byte) (int, error) {
	w.written = true
	return w.ResponseWriter.Write(b)
}

func (w *proxyResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *proxyResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func newProxy(url *url.URL) *proxyServer {
	p := httputil.NewSingleHostReverseProxy(url)

	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	if *flagSourceIP != "" {
		dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP(*flagSourceIP)}
	}

	p.Transport = &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 0,
		MaxIdleConns:          50,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
	}

	proxy := &proxyServer{
		upstreamProxy:  p,
		upstreamURL:    url,
		upstreamAPIKey: *flagUpstreamAPIKey,
		status:         notready,
		failCount:      0,
	}

	originalDirector := p.Director
	p.Director = func(req *http.Request) {
		originalDirector(req)

		if proxy.upstreamAPIKey == "" {
			return
		}

		req.Header.Set("Authorization", "Bearer "+proxy.upstreamAPIKey)
		req.Header.Set("x-api-key", proxy.upstreamAPIKey)
	}

	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if isClientDisconnect(err) {
			slog.Debug("client disconnected", "path", r.URL.Path, "error", err)
			return
		}

		slog.Warn("upstream error, marking not ready", "path", r.URL.Path, "error", err)
		proxy.setStatus(notready)
		proxy.cancelSSE()
		proxy.incFail(1)

		if wolErr := sendMagicPacket(*flagMac, *flagSourceIP); wolErr != nil {
			slog.Warn("failed to send magic WoL packet", "error", wolErr)
		}

		path := r.URL.Path
		if path == "/" || strings.HasPrefix(path, "/ui/") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, loadingPageHTML)
		}
	}

	go func() {
		eventsUrl := url.Scheme + "://" + url.Host + "/api/events"
		client := &http.Client{
			Timeout: 0,
			Transport: &http.Transport{
				DialContext:           dialer.DialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 0,
				IdleConnTimeout:       90 * time.Second,
			},
		}

		waitDuration := 10 * time.Second

		for {
			slog.Debug("connecting to SSE endpoint", "url", eventsUrl)

			ctx, cancel := context.WithCancel(context.Background())
			proxy.setSSECancel(cancel)

			req, err := http.NewRequestWithContext(ctx, "GET", eventsUrl, nil)
			if err != nil {
				cancel()
				slog.Warn("failed to create SSE request", "error", err)
				proxy.setStatus(notready)
				proxy.incFail(1)
				time.Sleep(waitDuration)
				continue
			}

			req.Header.Set("Accept", "text/event-stream")
			req.Header.Set("Cache-Control", "no-cache")
			req.Header.Set("Connection", "keep-alive")
			if proxy.upstreamAPIKey != "" {
				req.Header.Set("Authorization", "Bearer "+proxy.upstreamAPIKey)
				req.Header.Set("x-api-key", proxy.upstreamAPIKey)
			}

			resp, err := client.Do(req)
			if err != nil {
				cancel()
				if ctx.Err() == context.Canceled {
					slog.Info("SSE connection canceled, reconnecting")
					time.Sleep(2 * time.Second)
					continue
				}
				slog.Error("failed to connect to SSE endpoint", "error", err)
				proxy.setStatus(notready)
				proxy.incFail(1)
				time.Sleep(10 * time.Second)
				continue
			}

			if resp.StatusCode != http.StatusOK {
				cancel()
				if resp.StatusCode == http.StatusUnauthorized {
					if proxy.upstreamAPIKey == "" {
						slog.Warn("SSE endpoint returned 401; set -upstream-api-key if the upstream requires authentication")
					} else {
						slog.Warn("SSE endpoint returned 401; verify -upstream-api-key matches the upstream apiKeys configuration")
					}
				} else {
					slog.Warn("SSE endpoint returned non-OK status", "status", resp.StatusCode)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				proxy.setStatus(notready)
				proxy.incFail(1)
				time.Sleep(10 * time.Second)
				continue
			}

			slog.Info("connected to SSE endpoint, upstream ready")
			proxy.setStatus(ready)
			proxy.resetFailures()

			scanner := bufio.NewScanner(resp.Body)
			buf := make([]byte, 0, 1024*1024*2)
			scanner.Buffer(buf, 1024*1024*2)
			events := 0
			if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
				fmt.Print("Events: ")
			}
			for scanner.Scan() {
				if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
					events++
					fmt.Printf("%d, ", events)
				}
			}
			fmt.Println()
			cancel()

			scanErr := scanner.Err()
			if scanErr != nil && ctx.Err() != context.Canceled {
				slog.Error("error reading from SSE stream", "error", scanErr)
			}

			_ = resp.Body.Close()

			if ctx.Err() == context.Canceled {
				slog.Info("SSE connection canceled, reconnecting")
				time.Sleep(2 * time.Second)
			} else {
				slog.Info("SSE connection closed, upstream not ready")
				proxy.setStatus(notready)
				proxy.incFail(1)
				time.Sleep(waitDuration)
			}
		}
	}()

	return proxy
}

func (p *proxyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" && r.URL.Path == "/status" {
		status := string(p.getStatus())
		failCount := p.getFailures()
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		fmt.Fprintf(w, "status: %s\n", status)
		fmt.Fprintf(w, "failures: %d\n", failCount)
		return
	}

	if p.getStatus() == notready {
		path := r.URL.Path
		if strings.HasPrefix(path, "/api/events") {
			slog.Debug("Skipping wake up", "req", path)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		slog.Info("upstream not ready, sending magic packet", "req", path, "from", r.RemoteAddr)
		if err := sendMagicPacket(*flagMac, *flagSourceIP); err != nil {
			slog.Warn("failed to send magic WoL packet", "error", err)
		}

		if path == "/" || strings.HasPrefix(path, "/ui/") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, loadingPageHTML)
			return
		}

		ticker := time.NewTicker(250 * time.Millisecond)
		timeout, cancel := context.WithTimeout(context.Background(), time.Duration(*flagTimeout)*time.Second)
		defer cancel()
	loop:
		for {
			select {
			case <-timeout.Done():
				slog.Info("timeout waiting for upstream to be ready")
				http.Error(w, "timeout", http.StatusRequestTimeout)
				return
			case <-ticker.C:
				if p.getStatus() == ready {
					ticker.Stop()
					break loop
				}
			}
		}
	}

	const maxBodySize = 10 << 20 // 10MB
	var bodyBytes []byte
	if r.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(io.LimitReader(r.Body, maxBodySize))
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadGateway)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	pw := &proxyResponseWriter{ResponseWriter: w}
	p.upstreamProxy.ServeHTTP(pw, r)

	if pw.written {
		return
	}

	slog.Info("upstream failed while ready, waiting for recovery", "path", r.URL.Path)

	ticker := time.NewTicker(250 * time.Millisecond)
	timeout, cancel := context.WithTimeout(context.Background(), time.Duration(*flagTimeout)*time.Second)
	defer cancel()
	for {
		select {
		case <-timeout.Done():
			ticker.Stop()
			slog.Info("timeout waiting for upstream to be ready")
			http.Error(w, "timeout", http.StatusRequestTimeout)
			return
		case <-ticker.C:
			if p.getStatus() == ready {
				ticker.Stop()
				if bodyBytes != nil {
					r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				}
				p.upstreamProxy.ServeHTTP(w, r)
				return
			}
		}
	}
}

func (p *proxyServer) getStatus() upstreamStatus {
	p.statusMutex.RLock()
	defer p.statusMutex.RUnlock()
	return p.status
}

func (p *proxyServer) setStatus(status upstreamStatus) {
	p.statusMutex.Lock()
	defer p.statusMutex.Unlock()
	p.status = status
}

func (p *proxyServer) incFail(num int) {
	p.statusMutex.Lock()
	defer p.statusMutex.Unlock()
	p.failCount += num
}

func (p *proxyServer) getFailures() int {
	p.statusMutex.RLock()
	defer p.statusMutex.RUnlock()
	return p.failCount
}

func (p *proxyServer) resetFailures() {
	p.statusMutex.Lock()
	defer p.statusMutex.Unlock()
	p.failCount = 0
}

func (p *proxyServer) setSSECancel(cancel context.CancelFunc) {
	p.statusMutex.Lock()
	defer p.statusMutex.Unlock()
	p.sseCancel = cancel
}

func (p *proxyServer) cancelSSE() {
	p.statusMutex.RLock()
	cancel := p.sseCancel
	p.statusMutex.RUnlock()
	if cancel != nil {
		cancel()
	}
}

func isClientDisconnect(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer")
}

func sendMagicPacket(macAddr, sourceIP string) error {
	hwAddr, err := net.ParseMAC(macAddr)
	if err != nil {
		return err
	}

	if len(hwAddr) != 6 {
		return errors.New("invalid MAC address")
	}

	packet := make([]byte, 102)
	for i := 0; i < 6; i++ {
		packet[i] = 0xFF
	}
	for i := 1; i <= 16; i++ {
		copy(packet[i*6:], hwAddr)
	}

	dst := &net.UDPAddr{
		IP:   net.IPv4bcast,
		Port: 9,
	}

	var src *net.UDPAddr
	if sourceIP != "" {
		src = &net.UDPAddr{IP: net.ParseIP(sourceIP)}
	}

	conn, err := net.DialUDP("udp", src, dst)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Write(packet)
	return err
}
