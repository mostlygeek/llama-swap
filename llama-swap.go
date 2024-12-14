package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/proxy"
)

var version string = "0"
var commit string = "abcd1234"
var date = "unknown"

func main() {
	// Define a command-line flag for the port
	configPath := flag.String("config", "config.yaml", "config file name")
	listenStr := flag.String("listen", ":8080", "listen ip/port")
	showVersion := flag.Bool("version", false, "show version of build")

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

	if mode := os.Getenv("GIN_MODE"); mode != "" {
		gin.SetMode(mode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	proxyManager := proxy.New(config)
	fmt.Println("llama-swap listening on " + *listenStr)
	if err := proxyManager.Run(*listenStr); err != nil {
		fmt.Printf("Server error: %v\n", err)
		os.Exit(1)
	}
}
