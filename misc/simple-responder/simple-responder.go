package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
)

func main() {
	// Define a command-line flag for the port
	port := flag.String("port", "8080", "port to listen on")

	// Define a command-line flag for the response message
	responseMessage := flag.String("respond", "hi", "message to respond with")

	flag.Parse() // Parse the command-line flags

	// Set up the handler function using the provided response message
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Set the header to text/plain
		w.Header().Set("Content-Type", "text/plain")

		fmt.Fprintln(w, *responseMessage)

		// Get environment variables
		envVars := os.Environ()

		// Write each environment variable to the response
		for _, envVar := range envVars {
			fmt.Fprintln(w, envVar)
		}
	})

	// Set up the /health endpoint handler function
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		response := `{"status": "ok"}`
		w.Write([]byte(response))
	})

	address := "127.0.0.1:" + *port // Address with the specified port
	fmt.Printf("Server is listening on port %s\n", *port)

	// Start the server and log any error if it occurs
	if err := http.ListenAndServe(address, nil); err != nil {
		fmt.Printf("Error starting server: %s\n", err)
	}
}
