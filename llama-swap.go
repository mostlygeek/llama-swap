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
	listenStr := flag.String("listen", ":8080", "listen ip/port")
	showVersion := flag.Bool("version", false, "show version of build")
	watchConfig := flag.Bool("watch-config", false, "Automatically reload config file on change")

	flag.Parse() // Parse the command-line flags

	if *showVersion {
		fmt.Printf("version: %s (%s), built at %s\n", version, commit, date)
		os.Exit(0)
	}

	config, err := proxy.LoadConfig(*configPath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	if len(config.Profiles) > 0 {
		fmt.Println("WARNING: Profile functionality has been removed in favor of Groups. See the README for more information.")
	}

	if mode := os.Getenv("GIN_MODE"); mode != "" {
		gin.SetMode(mode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	proxyManager := proxy.New(config)

	// Setup channels for server management
	reloadChan := make(chan *proxy.ProxyManager)
	exitChan := make(chan struct{})
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create server with initial handler
	srv := &http.Server{
		Addr:    *listenStr,
		Handler: proxyManager,
	}

	// Start server
	fmt.Printf("llama-swap listening on %s\n", *listenStr)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Fatal server error: %v\n", err)
			close(exitChan)
		}
	}()

	// Handle config reloads and signals
	go func() {
		currentManager := proxyManager
		for {
			select {
			case newManager := <-reloadChan:
				log.Println("Config change detected, waiting for in-flight requests to complete...")
				// Stop old manager processes gracefully (this waits for in-flight requests)
				currentManager.StopProcesses()
				// Now do a full shutdown to clear the process map
				currentManager.Shutdown()
				currentManager = newManager
				srv.Handler = newManager
				log.Println("Server handler updated with new config")
			case sig := <-sigChan:
				fmt.Printf("Received signal %v, shutting down...\n", sig)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				currentManager.Shutdown()
				if err := srv.Shutdown(ctx); err != nil {
					fmt.Printf("Server shutdown error: %v\n", err)
				}
				close(exitChan)
				return
			}
		}
	}()

	// Start file watcher if requested
	if *watchConfig {
		absConfigPath, err := filepath.Abs(*configPath)
		if err != nil {
			log.Printf("Error getting absolute path for config: %v. File watching disabled.", err)
		} else {
			go watchConfigFileWithReload(absConfigPath, reloadChan)
		}
	}

	// Wait for exit signal
	<-exitChan
}

// watchConfigFileWithReload monitors the configuration file and sends new ProxyManager instances through reloadChan.
func watchConfigFileWithReload(configPath string, reloadChan chan<- *proxy.ProxyManager) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("Error creating file watcher: %v. File watching disabled.", err)
		return
	}
	defer watcher.Close()

	err = watcher.Add(configPath)
	if err != nil {
		log.Printf("Error adding config path (%s) to watcher: %v. File watching disabled.", configPath, err)
		return
	}

	log.Printf("Watching config file for changes: %s", configPath)

	var debounceTimer *time.Timer
	debounceDuration := 2 * time.Second

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			// We only care about writes to the specific config file
			if event.Name == configPath && event.Has(fsnotify.Write) {
				// Reset or start the debounce timer
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.AfterFunc(debounceDuration, func() {
					log.Printf("Config file modified: %s, reloading...", event.Name)

					// Try up to 3 times with exponential backoff
					var newConfig proxy.Config
					var err error
					for retries := 0; retries < 3; retries++ {
						// Load new configuration
						newConfig, err = proxy.LoadConfig(configPath)
						if err == nil {
							break
						}
						log.Printf("Error loading new config (attempt %d/3): %v", retries+1, err)
						if retries < 2 {
							time.Sleep(time.Duration(1<<retries) * time.Second)
						}
					}
					if err != nil {
						log.Printf("Failed to load new config after retries: %v", err)
						return
					}

					// Create new ProxyManager with new config
					newPM := proxy.New(newConfig)
					reloadChan <- newPM
					log.Println("Config reloaded successfully")
				})
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				log.Println("File watcher error channel closed.")
				return
			}
			log.Printf("File watcher error: %v", err)
		}
	}
}
