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
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/event"
	"github.com/mostlygeek/llama-swap/proxy"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

var (
	version string = "0"
	commit  string = "abcd1234"
	date    string = "unknown"
)

func main() {
	// Define a command-line flag for the port
	configPath := flag.String("config", "config.yaml", "config file name")
	listenStr := flag.String("listen", ":8080", "listen ip/port")
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

	// Setup channels for server management
	exitChan := make(chan struct{})
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create server with initial handler
	srv := &http.Server{
		Addr: *listenStr,
	}

	// Support for watching config and reloading when it changes
	reloadProxyManager := func() {
		if currentPM, ok := srv.Handler.(*proxy.ProxyManager); ok {
			conf, err = config.LoadConfig(*configPath)
			if err != nil {
				fmt.Printf("Warning, unable to reload configuration: %v\n", err)
				return
			}

			fmt.Println("Configuration Changed")
			currentPM.Shutdown()
			srv.Handler = proxy.New(conf)
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
			srv.Handler = proxy.New(conf)
		}
	}

	// load the initial proxy manager
	reloadProxyManager()
	debouncedReload := debounce(time.Second, reloadProxyManager)
	if *watchConfig {
		defer event.On(func(e proxy.ConfigFileChangedEvent) {
			if e.ReloadingState == proxy.ReloadingStateStart {
				debouncedReload()
			}
		})()

		fmt.Println("Watching Configuration for changes")
		go func() {
			absConfigPath, err := filepath.Abs(*configPath)
			if err != nil {
				fmt.Printf("Error getting absolute path for watching config file: %v\n", err)
				return
			}
			watcher, err := fsnotify.NewWatcher()
			if err != nil {
				fmt.Printf("Error creating file watcher: %v. File watching disabled.\n", err)
				return
			}

			configDir := filepath.Dir(absConfigPath)
			err = watcher.Add(configDir)
			if err != nil {
				fmt.Printf("Error adding config path directory (%s) to watcher: %v. File watching disabled.", configDir, err)
				return
			}

			defer watcher.Close()
			for {
				select {
				case changeEvent := <-watcher.Events:
					if changeEvent.Name == absConfigPath && (changeEvent.Has(fsnotify.Write) || changeEvent.Has(fsnotify.Create) || changeEvent.Has(fsnotify.Remove)) {
						event.Emit(proxy.ConfigFileChangedEvent{
							ReloadingState: proxy.ReloadingStateStart,
						})
					} else if changeEvent.Name == filepath.Join(configDir, "..data") && changeEvent.Has(fsnotify.Create) {
						// the change for k8s configmap
						event.Emit(proxy.ConfigFileChangedEvent{
							ReloadingState: proxy.ReloadingStateStart,
						})
					}

				case err := <-watcher.Errors:
					log.Printf("File watcher error: %v", err)
				}
			}
		}()
	}

	// shutdown on signal
	go func() {
		sig := <-sigChan
		fmt.Printf("Received signal %v, shutting down...\n", sig)
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
	}()

	// Start server
	fmt.Printf("llama-swap listening on %s\n", *listenStr)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Fatal server error: %v\n", err)
		}
	}()

	// Wait for exit signal
	<-exitChan
}

func debounce(interval time.Duration, f func()) func() {
	var timer *time.Timer
	return func() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(interval, f)
	}
}
