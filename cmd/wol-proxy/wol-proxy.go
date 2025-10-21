package main

import (
	"context"
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
	"sync"
	"time"
)

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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}
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

	// start a goroutien to check upstream status
	go func() {
		checkUrl := url.Scheme + "://" + url.Host + "/wol-health"
		client := &http.Client{Timeout: time.Second}
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {

			slog.Debug("checking upstream status at", "url", checkUrl)
			resp, err := client.Get(checkUrl)

			// drain the body
			if err == nil && resp != nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}

			if err == nil && resp != nil && resp.StatusCode == http.StatusOK {
				slog.Debug("upstream status: ready")
				proxy.setStatus(ready)
				proxy.statusMutex.Lock()
				proxy.failCount = 0
				proxy.statusMutex.Unlock()
			} else {
				slog.Debug("upstream status: notready", "error", err)
				proxy.setStatus(notready)
				proxy.statusMutex.Lock()
				proxy.failCount++
				proxy.statusMutex.Unlock()
			}

		}
	}()

	return proxy
}

func (p *proxyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" && r.URL.Path == "/status" {
		p.statusMutex.RLock()
		status := string(p.status)
		failCount := p.failCount
		p.statusMutex.RUnlock()
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		fmt.Fprintf(w, "status: %s\n", status)
		fmt.Fprintf(w, "failures: %d\n", failCount)
		return
	}

	if p.getStatus() == notready {
		slog.Info("upstream not ready, sending magic packet", "mac", *flagMac)
		if err := sendMagicPacket(*flagMac); err != nil {
			slog.Warn("failed to send magic WoL packet", "error", err)
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
