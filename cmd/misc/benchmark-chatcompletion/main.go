package main

// created for issue: #252 https://github.com/mostlygeek/llama-swap/issues/252
// this simple benchmark tool sends a lot of small chat completion requests to llama-swap
// to make sure all the requests are accounted for.
//
// requests can be sent in parallel, and the tool will report the results.
// usage: go run main.go -baseurl http://localhost:8080/v1 -model llama3 -requests 1000 -par 5

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

func main() {
	// ----- CLI arguments ----------------------------------------------------
	var (
		baseurl         string
		modelName       string
		totalRequests   int
		parallelization int
	)

	flag.StringVar(&baseurl, "baseurl", "http://localhost:8080/v1", "Base URL of the API (e.g., https://api.example.com)")
	flag.StringVar(&modelName, "model", "", "Model name to use")
	flag.IntVar(&totalRequests, "requests", 1, "Total number of requests to send")
	flag.IntVar(&parallelization, "par", 1, "Maximum number of concurrent requests")
	flag.Parse()

	if baseurl == "" || modelName == "" {
		fmt.Println("Error: both -baseurl and -model are required.")
		flag.Usage()
		os.Exit(1)
	}
	if totalRequests <= 0 {
		fmt.Println("Error: -requests must be greater than 0.")
		os.Exit(1)
	}
	if parallelization <= 0 {
		fmt.Println("Error: -parallelization must be greater than 0.")
		os.Exit(1)
	}

	// ----- HTTP client -------------------------------------------------------
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// ----- Tracking response codes -------------------------------------------
	statusCounts := make(map[int]int) // map[statusCode]count
	var mu sync.Mutex                 // protects statusCounts

	// ----- Request queue (buffered channel) ----------------------------------
	requests := make(chan int, 10) // Buffered channel with capacity 10

	// Goroutine to fill the request queue
	go func() {
		for i := 0; i < totalRequests; i++ {
			requests <- i + 1
		}
		close(requests)
	}()

	// ----- Worker pool -------------------------------------------------------
	var wg sync.WaitGroup
	for i := 0; i < parallelization; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for reqID := range requests {
				// Build request payload as a single line JSON string
				payload := `{"model":"` + modelName + `","max_tokens":100,"stream":false,"messages":[{"role":"user","content":"write a snake game in python"}]}`

				// Send POST request
				req, err := http.NewRequest(http.MethodPost,
					fmt.Sprintf("%s/chat/completions", baseurl),
					bytes.NewReader([]byte(payload)))
				if err != nil {
					log.Printf("[worker %d][req %d] request creation error: %v", workerID, reqID, err)
					mu.Lock()
					statusCounts[-1]++
					mu.Unlock()
					continue
				}
				req.Header.Set("Content-Type", "application/json")

				resp, err := client.Do(req)
				if err != nil {
					log.Printf("[worker %d][req %d] HTTP request error: %v", workerID, reqID, err)
					mu.Lock()
					statusCounts[-1]++
					mu.Unlock()
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				// Record status code
				mu.Lock()
				statusCounts[resp.StatusCode]++
				mu.Unlock()
			}
		}(i + 1)
	}

	// ----- Status ticker (prints every second) -------------------------------
	done := make(chan struct{})
	tickerDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		startTime := time.Now()
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				// Compute how many requests have completed so far
				completed := 0
				for _, cnt := range statusCounts {
					completed += cnt
				}
				// Calculate duration and progress
				duration := time.Since(startTime)
				progress := completed * 100 / totalRequests
				fmt.Printf("Duration: %v, Completed: %d%% requests\n", duration, progress)
				mu.Unlock()
			case <-done:
				duration := time.Since(startTime)
				fmt.Printf("Duration: %v, Completed: %d%% requests\n", duration, 100)
				close(tickerDone)
				return
			}
		}
	}()

	// Wait for all workers to finish
	wg.Wait()
	close(done)  // stops the status-update goroutine
	<-tickerDone // give ticker time to finish / print

	// ----- Summary ------------------------------------------------------------
	fmt.Println("\n\n=== HTTP response code summary ===")
	mu.Lock()
	for code, cnt := range statusCounts {
		if code == -1 {
			fmt.Printf("Client-side errors (no HTTP response): %d\n", cnt)
		} else {
			fmt.Printf("%d : %d\n", code, cnt)
		}
	}
	mu.Unlock()
}
