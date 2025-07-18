package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func main() {
	gin.SetMode(gin.TestMode)
	// Define a command-line flag for the port
	port := flag.String("port", "8080", "port to listen on")
	expectedModel := flag.String("model", "TheExpectedModel", "model name to expect")

	// Define a command-line flag for the response message
	responseMessage := flag.String("respond", "hi", "message to respond with")

	silent := flag.Bool("silent", false, "disable all logging")

	ignoreSigTerm := flag.Bool("ignore-sig-term", false, "ignore SIGTERM signal")

	flag.Parse() // Parse the command-line flags

	// Create a new Gin router
	r := gin.New()

	// Set up the handler function using the provided response message
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")

		// add a wait to simulate a slow query
		if wait, err := time.ParseDuration(c.Query("wait")); err == nil {
			time.Sleep(wait)
		}

		bodyBytes, _ := io.ReadAll(c.Request.Body)

		c.JSON(http.StatusOK, gin.H{
			"responseMessage":  *responseMessage,
			"h_content_length": c.Request.Header.Get("Content-Length"),
			"request_body":     string(bodyBytes),
		})
	})

	// for issue #62 to check model name strips profile slug
	// has to be one of the openAI API endpoints that llama-swap proxies
	// curl http://localhost:8080/v1/audio/speech -d '{"model":"profile:TheExpectedModel"}'
	r.POST("/v1/audio/speech", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read request body"})
			return
		}
		defer c.Request.Body.Close()
		modelName := gjson.GetBytes(body, "model").String()
		if modelName != *expectedModel {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid model: %s, expected: %s", modelName, *expectedModel)})
			return
		} else {
			c.JSON(http.StatusOK, gin.H{"message": "ok"})
		}
	})

	r.POST("/v1/completions", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.JSON(http.StatusOK, gin.H{
			"responseMessage": *responseMessage,
		})

	})

	// issue #41
	r.POST("/v1/audio/transcriptions", func(c *gin.Context) {
		// Parse the multipart form
		if err := c.Request.ParseMultipartForm(10 << 20); err != nil { // 10 MB max memory
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Error parsing multipart form: %s", err)})
			return
		}

		// Get the model from the form values
		model := c.Request.FormValue("model")

		if model == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing model parameter"})
			return
		}

		// Get the file from the form
		file, _, err := c.Request.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Error getting file: %s", err)})
			return
		}
		defer file.Close()

		// Read the file content to get its size
		fileBytes, err := io.ReadAll(file)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error reading file: %s", err)})
			return
		}

		fileSize := len(fileBytes)

		// Return a JSON response with the model and transcription text including file size
		c.JSON(http.StatusOK, gin.H{
			"text":  fmt.Sprintf("The length of the file is %d bytes", fileSize),
			"model": model,

			// expose some header values for testing
			"h_content_type":   c.GetHeader("Content-Type"),
			"h_content_length": c.GetHeader("Content-Length"),
		})
	})

	r.GET("/slow-respond", func(c *gin.Context) {
		echo := c.Query("echo")
		delay := c.Query("delay")

		if echo == "" {
			echo = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		}

		// Parse the duration
		if delay == "" {
			delay = "100ms"
		}

		t, err := time.ParseDuration(delay)
		if err != nil {
			c.Header("Content-Type", "text/plain")
			c.String(http.StatusBadRequest, fmt.Sprintf("Invalid duration: %s", err))
			return
		}

		c.Header("Content-Type", "text/plain")
		for _, char := range echo {
			c.Writer.Write([]byte(string(char)))
			c.Writer.Flush()

			// wait
			<-time.After(t)
		}
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

	srv := &http.Server{
		Addr:    address,
		Handler: r.Handler(),
	}

	// Disable logging if the --silent flag is set
	if *silent {
		gin.SetMode(gin.ReleaseMode)
		gin.DefaultWriter = io.Discard
		log.SetOutput(io.Discard)
	}

	if !*silent {
		fmt.Printf("My PID: %d\n", os.Getpid())
	}

	go func() {
		log.Printf("simple-responder listening on %s\n", address)
		// service connections
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("simple-responder err: %s\n", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server with
	// a timeout of 5 seconds.
	sigChan := make(chan os.Signal, 1)
	// kill (no param) default send syscall.SIGTERM
	// kill -2 is syscall.SIGINT
	// kill -9 is syscall.SIGKILL but can't be catch, so don't need add it
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	countSigInt := 0

runloop:
	for {
		signal := <-sigChan
		switch signal {
		case syscall.SIGINT:
			countSigInt++
			if countSigInt > 1 {
				break runloop
			} else {
				log.Println("Received SIGINT, send another SIGINT to shutdown")
			}
		case syscall.SIGTERM:
			if *ignoreSigTerm {
				log.Println("Ignoring SIGTERM")
			} else {
				log.Println("Received SIGTERM, shutting down")
				break runloop
			}
		default:
			break runloop
		}
	}

	log.Println("simple-responder shutting down")
}
