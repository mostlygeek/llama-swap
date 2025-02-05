package proxy

import (
	"context"
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

type ProcessState string

const (
	StateStopped  ProcessState = ProcessState("stopped")
	StateStarting ProcessState = ProcessState("starting")
	StateReady    ProcessState = ProcessState("ready")
	StateStopping ProcessState = ProcessState("stopping")

	// failed a health check on start and will not be recovered
	StateFailed ProcessState = ProcessState("failed")

	// process is shutdown and will not be restarted
	StateShutdown ProcessState = ProcessState("shutdown")
)

type Process struct {
	ID                 string
	config             ModelConfig
	cmd                *exec.Cmd
	logMonitor         *LogMonitor
	healthCheckTimeout int

	lastRequestHandled time.Time

	stateMutex sync.RWMutex
	state      ProcessState

	inFlightRequests sync.WaitGroup

	// used to block on multiple start() calls
	waitStarting sync.WaitGroup
}

func NewProcess(ID string, healthCheckTimeout int, config ModelConfig, logMonitor *LogMonitor) *Process {
	return &Process{
		ID:                 ID,
		config:             config,
		cmd:                nil,
		logMonitor:         logMonitor,
		healthCheckTimeout: healthCheckTimeout,
		state:              StateStopped,
	}
}

func (p *Process) setState(newState ProcessState) error {
	// enforce valid state transitions
	invalidTransition := false
	if p.state == StateStopped {
		// stopped -> starting
		if newState != StateStarting {
			invalidTransition = true
		}
	} else if p.state == StateStarting {
		// starting -> ready | failed | stopping
		if newState != StateReady && newState != StateFailed && newState != StateStopping {
			invalidTransition = true
		}
	} else if p.state == StateReady {
		// ready -> stopping
		if newState != StateStopping {
			invalidTransition = true
		}
	} else if p.state == StateStopping {
		// stopping -> stopped | shutdown
		if newState != StateStopped && newState != StateShutdown {
			invalidTransition = true
		}
	} else if p.state == StateFailed || p.state == StateShutdown {
		invalidTransition = true
	}

	if invalidTransition {
		//panic(fmt.Sprintf("Invalid state transition from %s to %s", p.state, newState))
		return fmt.Errorf("invalid state transition from %s to %s", p.state, newState)
	}

	p.state = newState
	return nil
}

func (p *Process) CurrentState() ProcessState {
	p.stateMutex.RLock()
	defer p.stateMutex.RUnlock()
	return p.state
}

// start starts the upstream command, checks the health endpoint, and sets the state to Ready
// it is a private method because starting is automatic but stopping can be called
// at any time.
func (p *Process) start() error {

	// wait for the other start() to complete
	curState := p.CurrentState()

	if curState == StateReady {
		return nil
	}

	if curState == StateStarting {
		p.waitStarting.Wait()

		if state := p.CurrentState(); state != StateReady {
			return fmt.Errorf("start() failed current state: %v", state)
		}

		return nil
	}

	p.stateMutex.Lock()
	defer p.stateMutex.Unlock()

	if err := p.setState(StateStarting); err != nil {
		return err
	}

	p.waitStarting.Add(1)
	defer p.waitStarting.Done()

	args, err := p.config.SanitizedCommand()
	if err != nil {
		return fmt.Errorf("unable to get sanitized command: %v", err)
	}

	p.cmd = exec.Command(args[0], args[1:]...)
	p.cmd.Stdout = p.logMonitor
	p.cmd.Stderr = p.logMonitor
	p.cmd.Env = p.config.Env

	err = p.cmd.Start()

	if err != nil {
		p.setState(StateFailed)
		return fmt.Errorf("start() failed: %v", err)
	}

	// One of three things can happen at this stage:
	// 1. The command exits unexpectedly
	// 2. The health check fails
	// 3. The health check passes
	//
	// only in the third case will the process be considered Ready to accept
	<-time.After(250 * time.Millisecond) // give process a bit of time to start

	if err := p.checkHealthEndpoint(); err != nil {
		return p.setState(StateFailed)
	}

	if p.config.UnloadAfter > 0 {
		// start a goroutine to check every second if
		// the process should be stopped
		go func() {
			maxDuration := time.Duration(p.config.UnloadAfter) * time.Second

			for range time.Tick(time.Second) {
				if p.state != StateReady {
					return
				}

				// wait for all inflight requests to complete and ticker
				p.inFlightRequests.Wait()

				if time.Since(p.lastRequestHandled) > maxDuration {
					fmt.Fprintf(p.logMonitor, "!!! Unloading model %s, TTL of %ds reached.\n", p.ID, p.config.UnloadAfter)
					p.Stop()
					return
				}
			}
		}()
	}

	return p.setState(StateReady)
}

func (p *Process) Stop() {
	// wait for any inflight requests before proceeding
	p.inFlightRequests.Wait()
	p.stateMutex.Lock()
	defer p.stateMutex.Unlock()

	// calling Stop() when state is invalid is a no-op
	if err := p.setState(StateStopping); err != nil {
		fmt.Fprintf(p.logMonitor, "!!! Info - Stop() err: %v\n", err)
		return
	}

	// stop the process with a graceful exit timeout
	p.stopCommand(5 * time.Second)

	if err := p.setState(StateStopped); err != nil {
		panic(fmt.Sprintf("Stop() failed to set state to stopped: %v", err))
	}
}

// Shutdown is called when llama-swap is shutting down. It will give a little bit
// of time for any inflight requests to complete before shutting down. If the Process
// is in the state of starting, it will cancel it and shut it down
func (p *Process) Shutdown() {

}

// stopCommand will send a SIGTERM to the process and wait for it to exit.
// If it does not exit within 5 seconds, it will send a SIGKILL.
func (p *Process) stopCommand(sigtermTTL time.Duration) {
	sigtermTimeout, cancelTimeout := context.WithTimeout(context.Background(), sigtermTTL)
	defer cancelTimeout()

	sigtermNormal := make(chan error, 1)
	go func() {
		sigtermNormal <- p.cmd.Wait()
	}()

	if p.cmd == nil || p.cmd.Process == nil {
		fmt.Println("huh")
		return
	}

	p.cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-sigtermTimeout.Done():
		fmt.Fprintf(p.logMonitor, "!!! process [%s] timed out waiting to stop, sending KILL signal\n", p.ID)
		p.cmd.Process.Kill()
	case err := <-sigtermNormal:
		if err != nil {
			if errno, ok := err.(syscall.Errno); ok {
				fmt.Fprintf(p.logMonitor, "!!! process [%s] errno >> %v\n", p.ID, errno)
			} else if exitError, ok := err.(*exec.ExitError); ok {
				if strings.Contains(exitError.String(), "signal: terminated") {
					fmt.Fprintf(p.logMonitor, "!!! process [%s] stopped OK\n", p.ID)
				} else if strings.Contains(exitError.String(), "signal: interrupt") {
					fmt.Fprintf(p.logMonitor, "!!! process [%s] interrupted OK\n", p.ID)
				} else {
					fmt.Fprintf(p.logMonitor, "!!! process [%s] ExitError >> %v, exit code: %d\n", p.ID, exitError, exitError.ExitCode())
				}

			} else {
				fmt.Fprintf(p.logMonitor, "!!! process [%s] exited >> %v\n", p.ID, err)
			}
		}
	}
}

func (p *Process) checkHealthEndpoint() error {
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

		resp, err := client.Do(req)

		ttl := (maxDuration - time.Since(startTime)).Seconds()

		if err != nil {
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

		time.Sleep(time.Second)
	}
}

func (p *Process) ProxyRequest(w http.ResponseWriter, r *http.Request) {

	// prevent new requests from being made while stopping or irrecoverable
	currentState := p.CurrentState()
	if currentState == StateFailed || currentState == StateShutdown || currentState == StateStopping {
		http.Error(w, fmt.Sprintf("Process can not ProxyRequest, state is %s", currentState), http.StatusServiceUnavailable)
		return
	}

	p.inFlightRequests.Add(1)
	defer func() {
		p.lastRequestHandled = time.Now()
		p.inFlightRequests.Done()
	}()

	// start the process on demand
	if p.CurrentState() != StateReady {
		if err := p.start(); err != nil {
			errstr := fmt.Sprintf("unable to start process: %s", err)
			http.Error(w, errstr, http.StatusInternalServerError)
			return
		}
	}

	proxyTo := p.config.Proxy
	client := &http.Client{}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, proxyTo+r.URL.String(), r.Body)
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
