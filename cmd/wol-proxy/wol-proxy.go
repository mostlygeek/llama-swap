package main

import (
	"bufio"
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
	flagMac      = flag.String("mac", "", "mac address to send WoL packet to")
	flagUpstream = flag.String("upstream", "", "upstream proxy address to send requests to")
	flagListen   = flag.String("listen", ":8080", "listen address to listen on")
	flagLog      = flag.String("log", "info", "log level (debug, info, warn, error)")
	flagTimeout  = flag.Int("timeout", 60, "seconds requests wait for upstream response before failing")
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

	// Validate flags
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
	// validate mac address
	if _, err = net.ParseMAC(*flagMac); err != nil {
		slog.Error("invalid mac address", "error", err)
		return
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

	proxy := newProxy(upstreamURL)
	server := &http.Server{
		Addr:    *flagListen,
		Handler: proxy,
	}

	// start the server
	go func() {
		slog.Info("server starting on", "address", *flagListen)
		if err := server.ListenAndServe(); err != nil {
			slog.Error("error starting server", "error", err)
		}
	}()

	// graceful shutdown
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
	upstreamProxy *httputil.ReverseProxy
	failCount     int
	statusMutex   sync.RWMutex
	status        upstreamStatus
}

func newProxy(url *url.URL) *proxyServer {
	p := httputil.NewSingleHostReverseProxy(url)
	proxy := &proxyServer{
		upstreamProxy: p,
		status:        notready,
		failCount:     0,
	}

	// start a goroutine to monitor upstream status via SSE
	go func() {
		eventsUrl := url.Scheme + "://" + url.Host + "/api/events"
		client := &http.Client{
			Timeout: 0, // No timeout for SSE connection
		}

		waitDuration := 10 * time.Second

		for {
			slog.Debug("connecting to SSE endpoint", "url", eventsUrl)

			req, err := http.NewRequest("GET", eventsUrl, nil)
			if err != nil {
				slog.Warn("failed to create SSE request", "error", err)
				proxy.setStatus(notready)
				proxy.incFail(1)
				time.Sleep(waitDuration)
				continue
			}

			req.Header.Set("Accept", "text/event-stream")
			req.Header.Set("Cache-Control", "no-cache")
			req.Header.Set("Connection", "keep-alive")

			resp, err := client.Do(req)
			if err != nil {
				slog.Error("failed to connect to SSE endpoint", "error", err)
				proxy.setStatus(notready)
				proxy.incFail(1)
				time.Sleep(10 * time.Second)
				continue
			}

			if resp.StatusCode != http.StatusOK {
				slog.Warn("SSE endpoint returned non-OK status", "status", resp.StatusCode)
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				proxy.setStatus(notready)
				proxy.incFail(1)
				time.Sleep(10 * time.Second)
				continue
			}

			// Successfully connected to SSE endpoint
			slog.Info("connected to SSE endpoint, upstream ready")
			proxy.setStatus(ready)
			proxy.resetFailures()

			// Read from the SSE stream to detect disconnection
			scanner := bufio.NewScanner(resp.Body)

			// use a fairly large buffer to avoid scanner errors when reading large SSE events
			buf := make([]byte, 0, 1024*1024*2)
			scanner.Buffer(buf, 1024*1024*2)
			events := 0
			if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
				fmt.Print("Events: ")
			}
			for scanner.Scan() {
				if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
					// Just read the events to keep connection alive
					// We don't need to process the event data
					events++
					fmt.Printf("%d, ", events)
				}
			}
			fmt.Println()
			if err := scanner.Err(); err != nil {
				slog.Error("error reading from SSE stream", "error", err)
			}

			// Connection closed or error occurred
			_ = resp.Body.Close()
			slog.Info("SSE connection closed, upstream not ready")
			proxy.setStatus(notready)
			proxy.incFail(1)

			// Wait before reconnecting
			time.Sleep(waitDuration)
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
		if err := sendMagicPacket(*flagMac); err != nil {
			slog.Warn("failed to send magic WoL packet", "error", err)
		}

		// For root or UI path requests, return loading page with status polling
		// the web page will do the polling and redirect when ready
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

	p.upstreamProxy.ServeHTTP(w, r)
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

func sendMagicPacket(macAddr string) error {
	hwAddr, err := net.ParseMAC(macAddr)
	if err != nil {
		return err
	}

	if len(hwAddr) != 6 {
		return errors.New("invalid MAC address")
	}

	// Create the magic packet.
	packet := make([]byte, 102)
	// Add 6 bytes of 0xFF.
	for i := 0; i < 6; i++ {
		packet[i] = 0xFF
	}
	// Repeat the MAC address 16 times.
	for i := 1; i <= 16; i++ {
		copy(packet[i*6:], hwAddr)
	}

	// Send the packet using UDP.
	addr := net.UDPAddr{
		IP:   net.IPv4bcast,
		Port: 9,
	}
	conn, err := net.DialUDP("udp", nil, &addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Write(packet)
	return err
}
