package main

import (
	"flag"
	"fmt"
	"net/http"
)

func main() {
	// Define a command-line flag for the port
	port := flag.String("port", "8080", "port to listen on")

	// Define a command-line flag for the response message
	responseMessage := flag.String("respond", "hi", "message to respond with")

	flag.Parse() // Parse the command-line flags

	// Set up the handler function using the provided response message
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, *responseMessage)
	})

	// Set up the /health endpoint handler function
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		response := `{"status": "ok"}`
		w.Write([]byte(response))
	})

	address := ":" + *port // Address with the specified port
	fmt.Printf("Server is listening on port %s\n", *port)

	// Start the server and log any error if it occurs
	if err := http.ListenAndServe(address, nil); err != nil {
		fmt.Printf("Error starting server: %s\n", err)
	}
}
