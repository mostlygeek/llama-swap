package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/event"
	"github.com/mostlygeek/llama-swap/proxy"
	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/mostlygeek/llama-swap/proxy/configwatcher"
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

	flag.Parse() // Parse the command-line flags

	if *showVersion {
		fmt.Printf("version: %s (%s), built at %s\n", version, commit, date)
		os.Exit(0)
	}

	conf, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	if len(conf.Profiles) > 0 {
		fmt.Println("WARNING: Profile functionality has been removed in favor of Groups. See the README for more information.")
	}

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

	// Setup channels for server management
	exitChan := make(chan struct{})
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// Context that bounds the lifetime of background watcher goroutines.
	watcherCtx, watcherCancel := context.WithCancel(context.Background())

	// Create server with initial handler
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

		fmt.Println("Reloading Configuration")
		if currentPM, ok := srv.Handler.(*proxy.ProxyManager); ok {
			conf, err = config.LoadConfig(*configPath)
			if err != nil {
				fmt.Printf("Warning, unable to reload configuration: %v\n", err)
				return
			}

			fmt.Println("Configuration Changed")
			currentPM.Shutdown()
			newPM := proxy.New(conf)
			newPM.SetVersion(date, commit, version)
			srv.Handler = newPM
			fmt.Println("Configuration Reloaded")

			// wait a few seconds and tell any UI to reload
			time.AfterFunc(3*time.Second, func() {
				event.Emit(proxy.ConfigFileChangedEvent{
					ReloadingState: proxy.ReloadingStateEnd,
				})
			})
		} else {
			conf, err = config.LoadConfig(*configPath)
			if err != nil {
				fmt.Printf("Error, unable to load configuration: %v\n", err)
				os.Exit(1)
			}
			newPM := proxy.New(conf)
			newPM.SetVersion(date, commit, version)
			srv.Handler = newPM
		}
	}

	// load the initial proxy manager
	reloadProxyManager()

	if *watchConfig {
		go func() {
			absConfigPath, err := filepath.Abs(*configPath)
			if err != nil {
				fmt.Printf("Error getting absolute path for watching config file: %v\n", err)
				return
			}
			fmt.Println("Watching configuration for changes (poll-based, 2s interval)")
			(&configwatcher.Watcher{
				Path:     absConfigPath,
				Interval: configwatcher.DefaultInterval,
				OnChange: func() {
					reloadProxyManager()
				},
			}).Run(watcherCtx)
		}()
	}

	// shutdown on signal
	// print the pid
	fmt.Printf("PID: %d\n", os.Getpid())
	go func() {
		for {
			sig := <-sigChan
			switch sig {
			case syscall.SIGHUP:
				fmt.Println("Received reload signal, reloading configuration")
				reloadProxyManager()
			case syscall.SIGINT, syscall.SIGTERM:
				fmt.Printf("Received signal %v, shutting down...\n", sig)
				watcherCancel()
				ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
				defer cancel()

				if pm, ok := srv.Handler.(*proxy.ProxyManager); ok {
					pm.Shutdown()
				} else {
					fmt.Println("srv.Handler is not of type *proxy.ProxyManager")
				}

				if err := srv.Shutdown(ctx); err != nil {
					fmt.Printf("Server shutdown error: %v\n", err)
				}
				close(exitChan)
			default:
				// do nothing on other signals
			}
		}
	}()

	// Start server
	go func() {
		var err error
		if useTLS {
			fmt.Printf("llama-swap listening with TLS on https://%s\n", *listenStr)
			err = srv.ListenAndServeTLS(*certFile, *keyFile)
		} else {
			fmt.Printf("llama-swap listening on http://%s\n", *listenStr)
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("Fatal server error: %v\n", err)
		}
	}()

	// Wait for exit signal
	<-exitChan
}
