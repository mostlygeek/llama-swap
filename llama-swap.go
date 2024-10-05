package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/mostlygeek/llama-swap/proxy"
)

func main() {
	// Define a command-line flag for the port
	configPath := flag.String("config", "config.yaml", "config file name")
	listenStr := flag.String("listen", ":8080", "listen ip/port")

	flag.Parse() // Parse the command-line flags

	config, err := proxy.LoadConfig(*configPath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	proxyManager := proxy.New(config)
	http.HandleFunc("/", proxyManager.HandleFunc)

	fmt.Println("llamagate listening on " + *listenStr)
	if err := http.ListenAndServe(*listenStr, nil); err != nil {
		fmt.Printf("Error starting server: %v\n", err)
		os.Exit(1)
	}
}
