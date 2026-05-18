package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/router"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

const shutdownTimeout = 30 * time.Second

var (
	flagConfig = flag.String("config", "", "path to config file (required)")
	flagListen = flag.String("listen", "0.0.0.0:8080", "listen address")
)

func main() {
	flag.Parse()

	if *flagConfig == "" {
		slog.Error("-config is required")
		os.Exit(1)
	}

	cfg, err := config.LoadConfig(*flagConfig)
	if err != nil {
		slog.Error("failed to load config", "path", *flagConfig, "error", err)
		os.Exit(1)
	}

	proxyLog := logmon.NewWriter(os.Stdout)
	upstreamLog := logmon.NewWriter(os.Stdout)

	switch strings.ToLower(strings.TrimSpace(cfg.LogLevel)) {
	case "debug":
		proxyLog.SetLogLevel(logmon.LevelDebug)
		upstreamLog.SetLogLevel(logmon.LevelDebug)
	case "warn":
		proxyLog.SetLogLevel(logmon.LevelWarn)
		upstreamLog.SetLogLevel(logmon.LevelWarn)
	case "error":
		proxyLog.SetLogLevel(logmon.LevelError)
		upstreamLog.SetLogLevel(logmon.LevelError)
	default:
		proxyLog.SetLogLevel(logmon.LevelInfo)
		upstreamLog.SetLogLevel(logmon.LevelInfo)
	}

	srv, err := router.NewServer(cfg, proxyLog, upstreamLog)
	if err != nil {
		slog.Error("failed to create router server", "error", err)
		os.Exit(1)
	}

	httpServer := &http.Server{
		Addr:    *flagListen,
		Handler: srv,
	}

	go func() {
		slog.Info("server starting", "address", *flagListen)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server error", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	stop()

	slog.Info("shutdown signal received, draining requests")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Warn("http server shutdown error", "error", err)
	}

	if err := srv.Shutdown(shutdownTimeout); err != nil {
		slog.Warn("router shutdown error", "error", err)
	}

	slog.Info("shutdown complete")
}
