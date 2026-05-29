package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/perf"
	"github.com/mostlygeek/llama-swap/internal/watcher"
	"github.com/mostlygeek/llama-swap/proxy"
)

var (
	version string = "0"
	commit  string = "abcd1234"
	date    string = "unknown"
)

func main() {
	// Define a command-line flag for the port
	configPath := flag.String("config", "config.yaml", "config file name")
	listenStr := flag.String("listen", "", "listen ip/port")
	certFile := flag.String("tls-cert-file", "", "TLS certificate file")
	keyFile := flag.String("tls-key-file", "", "TLS key file")
	showVersion := flag.Bool("version", false, "show version of build")
	watchConfig := flag.Bool("watch-config", false, "Automatically reload config file on change")
	mainLogger := logmon.New()

	flag.Parse() // Parse the command-line flags

	if *showVersion {
		fmt.Printf("version: %s (%s), built at %s", version, commit, date)
		os.Exit(0)
	}

	conf, err := config.LoadConfig(*configPath)
	if err != nil {
		mainLogger.Errorf("Error loading config: %v", err)
		os.Exit(1)
	}

	if len(conf.Profiles) > 0 {
		mainLogger.Warn("Profile functionality has been removed in favor of Groups. See the README for more information.")
	}

	switch strings.ToLower(strings.TrimSpace(conf.LogLevel)) {
	case "debug":
		mainLogger.SetLogLevel(logmon.LevelDebug)
	case "info":
		mainLogger.SetLogLevel(logmon.LevelInfo)
	case "warn":
		mainLogger.SetLogLevel(logmon.LevelWarn)
	case "error":
		mainLogger.SetLogLevel(logmon.LevelError)
	default:
		mainLogger.SetLogLevel(logmon.LevelInfo)
	}

	mainLogger.Debugf("PID: %d", os.Getpid())

	if mode := os.Getenv("GIN_MODE"); mode != "" {
		gin.SetMode(mode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	// Validate TLS flags.
	var useTLS = (*certFile != "" && *keyFile != "")
	if (*certFile != "" && *keyFile == "") ||
		(*certFile == "" && *keyFile != "") {
		fmt.Println("Error: Both --tls-cert-file and --tls-key-file must be provided for TLS.")
		os.Exit(1)
	}

	// Set default ports.
	if *listenStr == "" {
		defaultPort := ":8080"
		if useTLS {
			defaultPort = ":8443"
		}
		listenStr = &defaultPort
	}

	var mon *perf.Monitor
	if !conf.Performance.Disabled {
		mon, err = perf.New(conf.Performance, mainLogger)
		if err != nil {
			mainLogger.Errorf("failed to create monitor: %s", err.Error())
			os.Exit(1)
		}
		mon.Start()
	} else {
		mainLogger.Info("performance monitoring is disabled")
	}

	// Setup channels for server management
	exitChan := make(chan struct{})
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// Context that bounds the lifetime of background watcher goroutines.
	watcherCtx, watcherCancel := context.WithCancel(context.Background())

	// Create server with initial handlergit
	srv := &http.Server{
		Addr: *listenStr,
	}

	// Support for watching config and reloading when it changes
	reloading := false
	var reloadMutex sync.Mutex
	reloadProxyManager := func() {
		reloadMutex.Lock()
		if reloading {
			reloadMutex.Unlock()
			return
		}
		reloading = true
		reloadMutex.Unlock()
		defer func() {
			reloadMutex.Lock()
			reloading = false
			reloadMutex.Unlock()
		}()

		if currentPM, ok := srv.Handler.(*proxy.ProxyManager); ok {
			mainLogger.Info("Reloading Configuration")
			conf, err = config.LoadConfig(*configPath)
			if err != nil {
				mainLogger.Warnf("Unable to reload configuration: %v", err)
				return
			}

			mainLogger.Debug("Configuration Changed")
			currentPM.Shutdown()
			if mon != nil {
				mon.UpdateConfig(conf.Performance)
			}
			newPM := proxy.New(conf)
			newPM.SetVersion(date, commit, version)
			newPM.SetPerfMonitor(mon)
			srv.Handler = newPM
			mainLogger.Debug("Configuration Reloaded")

			// wait a few seconds and tell any UI to reload
			time.AfterFunc(3*time.Second, func() {
				event.Emit(proxy.ConfigFileChangedEvent{
					ReloadingState: proxy.ReloadingStateEnd,
				})
			})
		} else {
			conf, err = config.LoadConfig(*configPath)
			if err != nil {
				mainLogger.Errorf("Unable to load configuration: %v", err)
				os.Exit(1)
			}
			newPM := proxy.New(conf)
			newPM.SetVersion(date, commit, version)
			newPM.SetPerfMonitor(mon)
			srv.Handler = newPM
		}
	}

	// load the initial proxy manager
	reloadProxyManager()

	if *watchConfig {
		go func() {
			absConfigPath, err := filepath.Abs(*configPath)
			if err != nil {
				mainLogger.Errorf("watch-config unable to determine absolute path for watching config file: %v", err)
				return
			}
			mainLogger.Info("Watching configuration for changes (poll-based, 2s interval)")
			(&configwatcher.Watcher{
				Path:     absConfigPath,
				Interval: configwatcher.DefaultInterval,
				OnChange: func() {
					reloadProxyManager()
				},
			}).Run(watcherCtx)
		}()
	}

	// Signal handling
	go func() {
		for {
			sig := <-sigChan
			switch sig {
			case syscall.SIGHUP:
				mainLogger.Debug("Received SIGHUP")
				reloadProxyManager()
			case syscall.SIGINT, syscall.SIGTERM:
				mainLogger.Debugf("Received signal %v, shutting down...", sig)
				if mon != nil {
					mon.Stop()
				}
				watcherCancel()
				ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
				defer cancel()

				if pm, ok := srv.Handler.(*proxy.ProxyManager); ok {
					pm.Shutdown()
				} else {
					mainLogger.Error("srv.Handler is not of type *proxy.ProxyManager")
				}

				if err := srv.Shutdown(ctx); err != nil {
					mainLogger.Errorf("Server shutdown: %v", err)
				}
				close(exitChan)
				return
			default:
				// do nothing on other signals
			}
		}
	}()

	// Start server
	go func() {
		var err error
		if useTLS {
			mainLogger.Infof("llama-swap listening with TLS on https://%s", *listenStr)
			err = srv.ListenAndServeTLS(*certFile, *keyFile)
		} else {
			mainLogger.Infof("llama-swap listening on http://%s", *listenStr)
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			mainLogger.Errorf("Fatal server error: %v", err)
			os.Exit(1)
		}
	}()

	// Wait for exit signal
	<-exitChan
}
