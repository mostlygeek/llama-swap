package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Process struct {
	sync.Mutex

	ID                 string
	config             ModelConfig
	cmd                *exec.Cmd
	logMonitor         *LogMonitor
	healthCheckTimeout int

	isRunning bool
}

func NewProcess(ID string, healthCheckTimeout int, config ModelConfig, logMonitor *LogMonitor) *Process {
	return &Process{
		ID:                 ID,
		config:             config,
		cmd:                nil,
		logMonitor:         logMonitor,
		healthCheckTimeout: healthCheckTimeout,
	}
}

// start the process and check it for errors
func (p *Process) start() error {
	p.Lock()
	defer p.Unlock()

	if p.isRunning {
		return fmt.Errorf("process already running")
	}

	args, err := p.config.SanitizedCommand()
	if err != nil {
		return fmt.Errorf("unable to get sanitized command: %v", err)
	}

	p.cmd = exec.Command(args[0], args[1:]...)
	p.cmd.Stdout = p.logMonitor
	p.cmd.Stderr = p.logMonitor
	p.cmd.Env = p.config.Env

	err = p.cmd.Start()
	p.isRunning = true

	if err != nil {
		return err
	}

	// watch for the command to exit
	cmdCtx, cancel := context.WithCancelCause(context.Background())

	// monitor the command's exit status. Usually this happens if
	// the process exited unexpectedly
	go func() {
		err := p.cmd.Wait()
		if err != nil {
			cancel(fmt.Errorf("command [%s] %s", strings.Join(p.cmd.Args, " "), err.Error()))
		} else {
			cancel(nil)
		}

		p.isRunning = false
	}()

	// wait a bit for process to start before checking the health endpoint
	time.Sleep(250 * time.Millisecond)

	// wait for checkHealthEndpoint
	if err := p.checkHealthEndpoint(cmdCtx); err != nil {
		return err
	}

	return nil
}

func (p *Process) Stop() {
	p.Lock()
	defer p.Unlock()

	if !p.isRunning {
		return
	}

	p.cmd.Process.Signal(syscall.SIGTERM)
	p.cmd.Process.Wait()
	p.isRunning = false
}

func (p *Process) IsRunning() bool {
	return p.isRunning
}

func (p *Process) checkHealthEndpoint(cmdCtx context.Context) error {
	if p.config.Proxy == "" {
		return fmt.Errorf("no upstream available to check /health")
	}

	checkEndpoint := strings.TrimSpace(p.config.CheckEndpoint)

	if checkEndpoint == "none" {
		return nil
	}

	// keep default behaviour
	if checkEndpoint == "" {
		checkEndpoint = "/health"
	}

	proxyTo := p.config.Proxy
	maxDuration := time.Second * time.Duration(p.healthCheckTimeout)
	healthURL, err := url.JoinPath(proxyTo, checkEndpoint)
	if err != nil {
		return fmt.Errorf("failed to create health url with with %s and path %s", proxyTo, checkEndpoint)
	}

	client := &http.Client{}
	startTime := time.Now()

	for {
		req, err := http.NewRequest("GET", healthURL, nil)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmdCtx, time.Second)
		defer cancel()
		req = req.WithContext(ctx)
		resp, err := client.Do(req)

		ttl := (maxDuration - time.Since(startTime)).Seconds()

		if err != nil {
			// check if the context was cancelled
			select {
			case <-ctx.Done():
				err := context.Cause(ctx)
				if !errors.Is(err, context.DeadlineExceeded) {
					return err
				}
			default:
			}

			// wait a bit longer for TCP connection issues
			if strings.Contains(err.Error(), "connection refused") {
				fmt.Fprintf(p.logMonitor, "Connection refused on %s, ttl %.0fs\n", healthURL, ttl)
				time.Sleep(5 * time.Second)
			} else {
				time.Sleep(time.Second)
			}

			if ttl < 0 {
				return fmt.Errorf("failed to check health from: %s", healthURL)
			}

			continue
		}

		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}

		if ttl < 0 {
			return fmt.Errorf("failed to check health from: %s", healthURL)
		}
	}
}

func (p *Process) ProxyRequest(w http.ResponseWriter, r *http.Request) {
	if !p.isRunning {
		if err := p.start(); err != nil {
			errstr := fmt.Sprintf("unable to start process: %s", err)
			http.Error(w, errstr, http.StatusInternalServerError)
			return
		}
	}

	proxyTo := p.config.Proxy
	client := &http.Client{}
	req, err := http.NewRequest(r.Method, proxyTo+r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header = r.Header.Clone()
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// faster than io.Copy when streaming
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
	}
}
