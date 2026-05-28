package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <base-url> <model> [model...]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Example: %s http://localhost:8080 A B C D A B C D\n", os.Args[0])
		os.Exit(1)
	}

	baseURL := os.Args[1]
	models := os.Args[2:]

	// Chain of triggers ensures requests are sent in the order provided.
	triggers := make([]chan struct{}, len(models))
	for i := range triggers {
		triggers[i] = make(chan struct{}, 1)
	}
	triggers[0] <- struct{}{}

	var wg sync.WaitGroup
	start := time.Now()

	for i, model := range models {
		wg.Add(1)
		go func(idx int, m string) {
			defer wg.Done()

			<-triggers[idx]

			reqStart := time.Now()
			fmt.Printf("[%d] starting  model=%s\n", idx, m)

			if idx+1 < len(triggers) {
				triggers[idx+1] <- struct{}{}
			}

			err := sendRequest(baseURL, m)
			elapsed := time.Since(reqStart)
			total := time.Since(start)

			if err != nil {
				fmt.Printf("[%d] ERROR     model=%s elapsed=%s total=%s err=%v\n", idx, m, elapsed.Round(time.Millisecond), total.Round(time.Millisecond), err)
			} else {
				fmt.Printf("[%d] completed model=%s elapsed=%s total=%s\n", idx, m, elapsed.Round(time.Millisecond), total.Round(time.Millisecond))
			}
		}(i, model)
	}

	wg.Wait()
	fmt.Printf("all done in %s\n", time.Since(start).Round(time.Millisecond))
}

func sendRequest(baseURL, model string) error {
	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": "Say hello in one word."},
		},
		"max_tokens": 16,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := http.Post(baseURL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(b))
	}

	io.Copy(io.Discard, resp.Body)
	return nil
}
