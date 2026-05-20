package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/server"
	"github.com/mostlygeek/llama-swap/internal/shared"
	"github.com/mostlygeek/llama-swap/internal/watcher"
)

var (
	version = "0"
	commit  = "abcd1234"
	date    = "unknown"
)

const shutdownTimeout = 30 * time.Second

func main() {
	flagConfig := flag.String("config", "", "path to config file (required)")
	flagListen := flag.String("listen", "", "listen address (default :8080 or :8443 for TLS)")
	flagCertFile := flag.String("tls-cert-file", "", "TLS certificate file")
	flagKeyFile := flag.String("tls-key-file", "", "TLS key file")
	flagVersion := flag.Bool("version", false, "show version and exit")
	flagWatchConfig := flag.Bool("watch-config", false, "reload config on file change")
	flag.Parse()

	if *flagVersion {
		fmt.Printf("version: %s (%s), built at %s\n", version, commit, date)
		os.Exit(0)
	}

	if *flagConfig == "" {
		slog.Error("-config is required")
		os.Exit(1)
	}

	useTLS := *flagCertFile != "" || *flagKeyFile != ""
	if (*flagCertFile != "" && *flagKeyFile == "") || (*flagCertFile == "" && *flagKeyFile != "") {
		slog.Error("both -tls-cert-file and -tls-key-file must be provided for TLS")
		os.Exit(1)
	}

	listenAddr := *flagListen
	if listenAddr == "" {
		if useTLS {
			listenAddr = ":8443"
		} else {
			listenAddr = ":8080"
		}
	}

	proxyLog := logmon.NewWriter(os.Stdout)
	upstreamLog := logmon.NewWriter(os.Stdout)

	configPath := *flagConfig
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		slog.Error("failed to load config", "path", configPath, "error", err)
		os.Exit(1)
	}

	if len(cfg.Profiles) > 0 {
		proxyLog.Warn("Profile functionality has been removed in favor of Groups. See the README for more information.")
	}

	applyLogLevel := func(cfg config.Config) {
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
	}

	applyLogLevel(cfg)

	initialSrv, err := server.New(cfg, proxyLog, upstreamLog)
	if err != nil {
		slog.Error("failed to create server", "error", err)
		os.Exit(1)
	}

	// activeSrv is swapped atomically during hot reload.
	var activeMu sync.RWMutex
	activeSrv := initialSrv

	httpServer := &http.Server{
		Addr: listenAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			activeMu.RLock()
			srv := activeSrv
			activeMu.RUnlock()
			srv.ServeHTTP(w, r)
		}),
	}

	// reload guards against overlapping reloads triggered by concurrent signals
	// or file-watcher callbacks.
	var reloading bool
	var reloadMu sync.Mutex

	reload := func() {
		reloadMu.Lock()
		if reloading {
			reloadMu.Unlock()
			return
		}
		reloading = true
		reloadMu.Unlock()
		defer func() {
			reloadMu.Lock()
			reloading = false
			reloadMu.Unlock()
		}()

		proxyLog.Info("reloading configuration")

		newCfg, err := config.LoadConfig(configPath)
		if err != nil {
			proxyLog.Warnf("failed to reload config: %v", err)
			return
		}

		if len(newCfg.Profiles) > 0 {
			proxyLog.Warn("Profile functionality has been removed in favor of Groups. See the README for more information.")
		}

		newSrv, err := server.New(newCfg, proxyLog, upstreamLog)
		if err != nil {
			proxyLog.Warnf("failed to build new server during reload: %v", err)
			return
		}

		activeMu.Lock()
		old := activeSrv
		activeSrv = newSrv
		activeMu.Unlock()

		applyLogLevel(newCfg)

		if err := old.Shutdown(shutdownTimeout); err != nil {
			proxyLog.Warnf("error shutting down old server during reload: %v", err)
		}

		// Notify UI after a short delay so it can refresh model state.
		time.AfterFunc(3*time.Second, func() {
			event.Emit(shared.ConfigFileChangedEvent{State: shared.ReloadingStateEnd})
		})

		proxyLog.Info("configuration reloaded")
	}

	watcherCtx, watcherCancel := context.WithCancel(context.Background())
	defer watcherCancel()

	if *flagWatchConfig {
		absConfigPath, err := filepath.Abs(configPath)
		if err != nil {
			slog.Error("watch-config: failed to resolve config path", "error", err)
			os.Exit(1)
		}
		proxyLog.Info("watching configuration for changes (poll-based, 2s interval)")
		go func() {
			(&configwatcher.Watcher{
				Path:     absConfigPath,
				Interval: configwatcher.DefaultInterval,
				OnChange: reload,
			}).Run(watcherCtx)
		}()
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		var startErr error
		if useTLS {
			proxyLog.Infof("llama-swap listening with TLS on https://%s", listenAddr)
			startErr = httpServer.ListenAndServeTLS(*flagCertFile, *flagKeyFile)
		} else {
			proxyLog.Infof("llama-swap listening on http://%s", listenAddr)
			startErr = httpServer.ListenAndServe()
		}
		if startErr != nil && !errors.Is(startErr, http.ErrServerClosed) {
			slog.Error("http server error", "error", startErr)
			os.Exit(1)
		}
	}()

	exitChan := make(chan struct{})

	go func() {
		for {
			sig := <-sigChan
			switch sig {
			case syscall.SIGHUP:
				proxyLog.Info("received SIGHUP, reloading config")
				go reload()
			case syscall.SIGINT, syscall.SIGTERM:
				proxyLog.Infof("received signal %v, shutting down", sig)
				watcherCancel()

				shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
				defer cancel()
				if err := httpServer.Shutdown(shutdownCtx); err != nil {
					proxyLog.Warnf("http server shutdown error: %v", err)
				}

				activeMu.RLock()
				srv := activeSrv
				activeMu.RUnlock()
				if err := srv.Shutdown(shutdownTimeout); err != nil {
					proxyLog.Warnf("router shutdown error: %v", err)
				}

				close(exitChan)
				return
			}
		}
	}()

	<-exitChan
	proxyLog.Info("shutdown complete")
}
