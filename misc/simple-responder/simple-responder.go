package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/gin-gonic/gin"
)

func main() {
	gin.SetMode(gin.TestMode)
	// Define a command-line flag for the port
	port := flag.String("port", "8080", "port to listen on")

	// Define a command-line flag for the response message
	responseMessage := flag.String("respond", "hi", "message to respond with")

	flag.Parse() // Parse the command-line flags

	// Create a new Gin router
	r := gin.New()

	// Set up the handler function using the provided response message
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain")
		c.String(200, *responseMessage)
	})

	r.POST("/v1/completions", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain")
		c.String(200, *responseMessage)
	})

	r.GET("/test", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain")
		c.String(200, *responseMessage)
	})

	r.GET("/env", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain")
		c.String(200, *responseMessage)

		// Get environment variables
		envVars := os.Environ()

		// Write each environment variable to the response
		for _, envVar := range envVars {
			c.String(200, envVar)
		}
	})

	// Set up the /health endpoint handler function
	r.GET("/health", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.JSON(200, gin.H{"status": "ok"})
	})

	r.GET("/", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain")
		c.String(200, fmt.Sprintf("%s %s", c.Request.Method, c.Request.URL.Path))
	})

	address := "127.0.0.1:" + *port // Address with the specified port
	fmt.Printf("Server is listening on port %s\n", *port)

	// Start the server and log any error if it occurs
	if err := r.Run(address); err != nil {
		log.Printf("Error starting server: %s\n", err)
	}
}
