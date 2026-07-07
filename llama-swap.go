package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
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
	"github.com/mostlygeek/llama-swap/internal/perf"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/server"
	"github.com/mostlygeek/llama-swap/internal/shared"
	"github.com/mostlygeek/llama-swap/internal/store"
	"github.com/mostlygeek/llama-swap/internal/watcher"
)

var (
	version = "0"
	commit  = "abcd1234"
	date    = "unknown"
)

const shutdownTimeout = 30 * time.Second

// logTimeFormats maps the cfg.LogTimeFormat value to a Go time layout. An
// unset or unrecognised value yields "" — no timestamp prefix.
var logTimeFormats = map[string]string{
	"ansic":       time.ANSIC,
	"unixdate":    time.UnixDate,
	"rubydate":    time.RubyDate,
	"rfc822":      time.RFC822,
	"rfc822z":     time.RFC822Z,
	"rfc850":      time.RFC850,
	"rfc1123":     time.RFC1123,
	"rfc1123z":    time.RFC1123Z,
	"rfc3339":     time.RFC3339,
	"rfc3339nano": time.RFC3339Nano,
	"kitchen":     time.Kitchen,
	"stamp":       time.Stamp,
	"stampmilli":  time.StampMilli,
	"stampmicro":  time.StampMicro,
	"stampnano":   time.StampNano,
}

func configStorePath(cfg config.Config) string {
	if cfg.Store == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Store.Path)
}

func main() {
	flagConfig := flag.String("config", "", "path to config file")
	flagConfigDir := flag.String("config-dir", "", "directory of *.yml/*.yaml config files (additive to -config)")
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

	if *flagConfig == "" && *flagConfigDir == "" {
		slog.Error("at least one of -config or -config-dir must be provided")
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

	cfg, err := config.LoadConfigSources(*flagConfig, *flagConfigDir)
	if err != nil {
		slog.Error("failed to load config", "config", *flagConfig, "config-dir", *flagConfigDir, "error", err)
		os.Exit(1)
	}

	// Loggers are wired per cfg.LogToStdout: proxy/upstream feed muxLog, which
	// owns the combined history served by /logs. They outlive config reloads,
	// so a LogToStdout change requires a restart to take effect.
	muxLog, proxyLog, upstreamLog := server.NewLoggers(cfg.LogToStdout)

	if len(cfg.Profiles) > 0 {
		proxyLog.Warn("Profile functionality has been removed in favor of Groups. See the README for more information.")
	}

	applyLogSettings := func(cfg config.Config) {
		level := logmon.LevelInfo
		switch strings.ToLower(strings.TrimSpace(cfg.LogLevel)) {
		case "debug":
			level = logmon.LevelDebug
		case "warn":
			level = logmon.LevelWarn
		case "error":
			level = logmon.LevelError
		}
		timeFormat := logTimeFormats[strings.ToLower(strings.TrimSpace(cfg.LogTimeFormat))]
		for _, lg := range []*logmon.Monitor{proxyLog, upstreamLog} {
			lg.SetLogLevel(level)
			lg.SetLogTimeFormat(timeFormat)
		}
	}

	applyLogSettings(cfg)
	proxyLog.Debugf("PID: %d", os.Getpid())

	// On Windows, bind the process tree to a Job Object so every upstream
	// process is reaped when llama-swap exits — even on a forced kill. No-op
	// elsewhere. Non-fatal: a failure just falls back to per-process teardown.
	if err := process.SetupTreeCleanup(); err != nil {
		proxyLog.Warnf("failed to set up process tree cleanup: %v", err)
	}

	// perfMon outlives config reloads; its config is updated in place.
	var perfMon *perf.Monitor
	if !cfg.Performance.Disabled {
		perfMon, err = perf.New(cfg.Performance, proxyLog)
		if err != nil {
			slog.Error("failed to create performance monitor", "error", err)
			os.Exit(1)
		}
		perfMon.Start()
	} else {
		proxyLog.Info("performance monitoring is disabled")
	}

	buildInfo := server.BuildInfo{Version: version, Commit: commit, Date: date}

	initialStorePath := configStorePath(cfg)
	initialStore, err := store.New(initialStorePath)
	if err != nil {
		slog.Error("failed to create store", "error", err)
		os.Exit(1)
	}

	initialSrv, err := server.New(cfg, muxLog, proxyLog, upstreamLog, perfMon, initialStore, buildInfo)
	if err != nil {
		slog.Error("failed to create server", "error", err)
		initialStore.Close()
		os.Exit(1)
	}

	// activeSrv is swapped atomically during hot reload.
	var activeMu sync.RWMutex
	activeSrv := initialSrv
	activeStore := initialStore
	activeStorePath := initialStorePath

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

		newCfg, err := config.LoadConfigSources(*flagConfig, *flagConfigDir)
		if err != nil {
			proxyLog.Warnf("failed to reload config: %v", err)
			return
		}

		if len(newCfg.Profiles) > 0 {
			proxyLog.Warn("Profile functionality has been removed in favor of Groups. See the README for more information.")
		}

		if perfMon != nil {
			perfMon.UpdateConfig(newCfg.Performance)
		}

		newStorePath := configStorePath(newCfg)
		activeMu.RLock()
		currentStore := activeStore
		currentStorePath := activeStorePath
		activeMu.RUnlock()

		newStore := currentStore
		storeChanged := newStorePath != currentStorePath
		if storeChanged {
			newStore, err = store.New(newStorePath)
			if err != nil {
				proxyLog.Warnf("failed to create new store during reload: %v", err)
				return
			}
		}

		newSrv, err := server.New(newCfg, muxLog, proxyLog, upstreamLog, perfMon, newStore, buildInfo)
		if err != nil {
			proxyLog.Warnf("failed to build new server during reload: %v", err)
			if storeChanged {
				newStore.Close()
			}
			return
		}

		activeMu.Lock()
		old := activeSrv
		oldStore := activeStore
		activeSrv = newSrv
		activeStore = newStore
		activeStorePath = newStorePath
		activeMu.Unlock()

		applyLogSettings(newCfg)

		if err := old.Shutdown(shutdownTimeout); err != nil {
			proxyLog.Warnf("error shutting down old server during reload: %v", err)
		}
		if storeChanged {
			if err := oldStore.Close(); err != nil {
				proxyLog.Warnf("error closing old store during reload: %v", err)
			}
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
		proxyLog.Info("watching configuration for changes (poll-based, 2s interval)")

		if *flagConfig != "" {
			absConfigPath, err := filepath.Abs(*flagConfig)
			if err != nil {
				slog.Error("watch-config: failed to resolve config path", "error", err)
				os.Exit(1)
			}
			go func() {
				(&configwatcher.Watcher{
					Path:     absConfigPath,
					Interval: configwatcher.DefaultInterval,
					OnChange: reload,
				}).Run(watcherCtx)
			}()
		}

		if *flagConfigDir != "" {
			absConfigDir, err := filepath.Abs(*flagConfigDir)
			if err != nil {
				slog.Error("watch-config: failed to resolve config-dir path", "error", err)
				os.Exit(1)
			}
			go func() {
				(&configwatcher.DirWatcher{
					Path:     absConfigDir,
					Interval: configwatcher.DefaultInterval,
					OnChange: reload,
				}).Run(watcherCtx)
			}()
		}
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

	if !shared.IsLoopbackAddr(listenAddr) {
		_, port, _ := net.SplitHostPort(listenAddr)
		proxyLog.Infof("llama-swap is reachable by all hosts on the network, use -listen localhost:%s to restrict to loopback only", port)
	}

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

				// Backstop against a stalled shutdown: force the process to
				// exit once the whole graceful sequence has had its full budget.
				// On Windows the Job Object reaps upstream processes on exit, so
				// a forced exit still cleans up rather than orphaning children.
				go func() {
					time.Sleep(shutdownTimeout + 5*time.Second)
					proxyLog.Warnf("graceful shutdown exceeded %v, forcing exit", shutdownTimeout)
					os.Exit(1)
				}()

				activeMu.RLock()
				srv := activeSrv
				st := activeStore
				activeMu.RUnlock()

				// Close long-lived SSE streams first so httpServer.Shutdown can
				// drain without blocking on them for the full timeout.
				srv.CloseStreams()

				// Both phases share a single deadline so total shutdown is
				// bounded by shutdownTimeout rather than 2x it.
				deadline := time.Now().Add(shutdownTimeout)
				shutdownCtx, cancel := context.WithDeadline(context.Background(), deadline)
				defer cancel()
				if err := httpServer.Shutdown(shutdownCtx); err != nil {
					proxyLog.Warnf("http server shutdown error: %v", err)
				}

				// Clamp the remaining budget to a small positive value: a
				// non-positive timeout makes the router fall back to its own
				// healthCheckTimeout, which would defeat the shared deadline.
				remaining := time.Until(deadline)
				if remaining <= 0 {
					remaining = time.Millisecond
				}
				if err := srv.Shutdown(remaining); err != nil {
					proxyLog.Warnf("router shutdown error: %v", err)
				}

				if perfMon != nil {
					perfMon.Stop()
				}
				if err := st.Close(); err != nil {
					proxyLog.Warnf("store shutdown error: %v", err)
				}

				close(exitChan)
				return
			}
		}
	}()

	<-exitChan
	proxyLog.Info("shutdown complete")
}
