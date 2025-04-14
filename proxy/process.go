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
	ID     string
	config ModelConfig
	cmd    *exec.Cmd

	processLogger *LogMonitor
	proxyLogger   *LogMonitor

	healthCheckTimeout      int
	healthCheckLoopInterval time.Duration

	lastRequestHandled time.Time

	stateMutex sync.RWMutex
	state      ProcessState

	inFlightRequests sync.WaitGroup

	// used to block on multiple start() calls
	waitStarting sync.WaitGroup

	// for managing shutdown state
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
}

func NewProcess(ID string, healthCheckTimeout int, config ModelConfig, processLogger *LogMonitor, proxyLogger *LogMonitor) *Process {
	ctx, cancel := context.WithCancel(context.Background())
	return &Process{
		ID:                      ID,
		config:                  config,
		cmd:                     nil,
		processLogger:           processLogger,
		proxyLogger:             proxyLogger,
		healthCheckTimeout:      healthCheckTimeout,
		healthCheckLoopInterval: 5 * time.Second, /* default, can not be set by user - used for testing */
		state:                   StateStopped,
		shutdownCtx:             ctx,
		shutdownCancel:          cancel,
	}
}

// LogMonitor returns the log monitor associated with the process.
func (p *Process) LogMonitor() *LogMonitor {
	return p.processLogger
}

// custom error types for swapping state
var (
	ErrExpectedStateMismatch  = errors.New("expected state mismatch")
	ErrInvalidStateTransition = errors.New("invalid state transition")
)

// swapState performs a compare and swap of the state atomically. It returns the current state
// and an error if the swap failed.
func (p *Process) swapState(expectedState, newState ProcessState) (ProcessState, error) {
	p.stateMutex.Lock()
	defer p.stateMutex.Unlock()

	if p.state != expectedState {
		return p.state, ErrExpectedStateMismatch
	}

	if !isValidTransition(p.state, newState) {
		p.proxyLogger.Warnf("Invalid state transition from %s to %s", p.state, newState)
		return p.state, ErrInvalidStateTransition
	}

	p.proxyLogger.Debugf("State transition from %s to %s", expectedState, newState)
	p.state = newState
	return p.state, nil
}

// Helper function to encapsulate transition rules
func isValidTransition(from, to ProcessState) bool {
	switch from {
	case StateStopped:
		return to == StateStarting
	case StateStarting:
		return to == StateReady || to == StateFailed || to == StateStopping
	case StateReady:
		return to == StateStopping
	case StateStopping:
		return to == StateStopped || to == StateShutdown
	case StateFailed, StateShutdown:
		return false // No transitions allowed from these states
	}
	return false
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

	if p.config.Proxy == "" {
		return fmt.Errorf("can not start(), upstream proxy missing")
	}

	args, err := p.config.SanitizedCommand()
	if err != nil {
		return fmt.Errorf("unable to get sanitized command: %v", err)
	}

	if curState, err := p.swapState(StateStopped, StateStarting); err != nil {
		if err == ErrExpectedStateMismatch {
			// already starting, just wait for it to complete and expect
			// it to be be in the Ready start after. If not, return an error
			if curState == StateStarting {
				p.waitStarting.Wait()
				if state := p.CurrentState(); state == StateReady {
					return nil
				} else {
					return fmt.Errorf("process was already starting but wound up in state %v", state)
				}
			} else {
				return fmt.Errorf("processes was in state %v when start() was called", curState)
			}
		} else {
			return fmt.Errorf("failed to set Process state to starting: current state: %v, error: %v", curState, err)
		}
	}

	p.waitStarting.Add(1)
	defer p.waitStarting.Done()

	p.cmd = exec.Command(args[0], args[1:]...)
	p.cmd.Stdout = p.processLogger
	p.cmd.Stderr = p.processLogger
	p.cmd.Env = p.config.Env

	err = p.cmd.Start()

	// Set process state to failed
	if err != nil {
		if curState, swapErr := p.swapState(StateStarting, StateFailed); swapErr != nil {
			return fmt.Errorf(
				"failed to start command and state swap failed. command error: %v, current state: %v, state swap error: %v",
				err, curState, swapErr,
			)
		}
		return fmt.Errorf("start() failed: %v", err)
	}

	// One of three things can happen at this stage:
	// 1. The command exits unexpectedly
	// 2. The health check fails
	// 3. The health check passes
	//
	// only in the third case will the process be considered Ready to accept
	<-time.After(250 * time.Millisecond) // give process a bit of time to start

	checkStartTime := time.Now()
	maxDuration := time.Second * time.Duration(p.healthCheckTimeout)
	checkEndpoint := strings.TrimSpace(p.config.CheckEndpoint)

	// a "none" means don't check for health ... I could have picked a better word :facepalm:
	if checkEndpoint != "none" {
		// keep default behaviour
		if checkEndpoint == "" {
			checkEndpoint = "/health"
		}

		proxyTo := p.config.Proxy
		healthURL, err := url.JoinPath(proxyTo, checkEndpoint)
		if err != nil {
			return fmt.Errorf("failed to create health check URL proxy=%s and checkEndpoint=%s", proxyTo, checkEndpoint)
		}

		checkDeadline, cancelHealthCheck := context.WithDeadline(
			context.Background(),
			checkStartTime.Add(maxDuration),
		)
		defer cancelHealthCheck()

	loop:
		// Ready Check loop
		for {
			select {
			case <-checkDeadline.Done():
				if curState, err := p.swapState(StateStarting, StateFailed); err != nil {
					return fmt.Errorf("health check timed out after %vs AND state swap failed: %v, current state: %v", maxDuration.Seconds(), err, curState)
				} else {
					return fmt.Errorf("health check timed out after %vs", maxDuration.Seconds())
				}
			case <-p.shutdownCtx.Done():
				return errors.New("health check interrupted due to shutdown")
			default:
				if err := p.checkHealthEndpoint(healthURL); err == nil {
					p.proxyLogger.Infof("Health check passed on %s", healthURL)
					cancelHealthCheck()
					break loop
				} else {
					if strings.Contains(err.Error(), "connection refused") {
						endTime, _ := checkDeadline.Deadline()
						ttl := time.Until(endTime)
						p.proxyLogger.Infof("Connection refused on %s, retrying in %.0fs", healthURL, ttl.Seconds())
					} else {
						p.proxyLogger.Infof("Health check error on %s, %v", healthURL, err)
					}
				}
			}

			<-time.After(p.healthCheckLoopInterval)
		}
	}

	if p.config.UnloadAfter > 0 {
		// start a goroutine to check every second if
		// the process should be stopped
		go func() {
			maxDuration := time.Duration(p.config.UnloadAfter) * time.Second

			for range time.Tick(time.Second) {
				if p.CurrentState() != StateReady {
					return
				}

				// wait for all inflight requests to complete and ticker
				p.inFlightRequests.Wait()

				if time.Since(p.lastRequestHandled) > maxDuration {

					p.proxyLogger.Infof("Unloading model %s, TTL of %ds reached.", p.ID, p.config.UnloadAfter)
					p.Stop()
					return
				}
			}
		}()
	}

	if curState, err := p.swapState(StateStarting, StateReady); err != nil {
		return fmt.Errorf("failed to set Process state to ready: current state: %v, error: %v", curState, err)
	} else {
		return nil
	}
}

func (p *Process) Stop() {
	// wait for any inflight requests before proceeding
	p.inFlightRequests.Wait()

	// calling Stop() when state is invalid is a no-op
	if curState, err := p.swapState(StateReady, StateStopping); err != nil {
		p.proxyLogger.Infof("Stop() Ready -> StateStopping err: %v, current state: %v", err, curState)
		return
	}

	// stop the process with a graceful exit timeout
	p.stopCommand(5 * time.Second)

	if curState, err := p.swapState(StateStopping, StateStopped); err != nil {
		p.proxyLogger.Infof("Stop() StateStopping -> StateStopped err: %v, current state: %v", err, curState)
	}
}

// Shutdown is called when llama-swap is shutting down. It will give a little bit
// of time for any inflight requests to complete before shutting down. If the Process
// is in the state of starting, it will cancel it and shut it down
func (p *Process) Shutdown() {
	p.shutdownCancel()
	p.stopCommand(5 * time.Second)
	p.state = StateShutdown
}

// stopCommand will send a SIGTERM to the process and wait for it to exit.
// If it does not exit within 5 seconds, it will send a SIGKILL.
func (p *Process) stopCommand(sigtermTTL time.Duration) {
	stopStartTime := time.Now()
	defer func() {
		p.proxyLogger.Debugf("Process [%s] stopCommand took %v", p.ID, time.Since(stopStartTime))
	}()

	sigtermTimeout, cancelTimeout := context.WithTimeout(context.Background(), sigtermTTL)
	defer cancelTimeout()

	sigtermNormal := make(chan error, 1)
	go func() {
		sigtermNormal <- p.cmd.Wait()
	}()

	if p.cmd == nil || p.cmd.Process == nil {
		p.proxyLogger.Warnf("Process [%s] cmd or cmd.Process is nil", p.ID)
		return
	}

	if err := p.terminateProcess(); err != nil {
		p.proxyLogger.Infof("Failed to gracefully terminate process [%s]: %v", p.ID, err)
	}

	select {
	case <-sigtermTimeout.Done():
		p.proxyLogger.Infof("Process [%s] timed out waiting to stop, sending KILL signal", p.ID)
		p.cmd.Process.Kill()
	case err := <-sigtermNormal:
		if err != nil {
			if errno, ok := err.(syscall.Errno); ok {
				p.proxyLogger.Errorf("Process [%s] errno >> %v", p.ID, errno)
			} else if exitError, ok := err.(*exec.ExitError); ok {
				if strings.Contains(exitError.String(), "signal: terminated") {
					p.proxyLogger.Infof("Process [%s] stopped OK", p.ID)
				} else if strings.Contains(exitError.String(), "signal: interrupt") {
					p.proxyLogger.Infof("Process [%s] interrupted OK", p.ID)
				} else {
					p.proxyLogger.Warnf("Process [%s] ExitError >> %v, exit code: %d", p.ID, exitError, exitError.ExitCode())
				}
			} else {
				p.proxyLogger.Errorf("Process [%s] exited >> %v", p.ID, err)
			}
		}
	}
}

func (p *Process) checkHealthEndpoint(healthURL string) error {

	client := &http.Client{
		Timeout: 500 * time.Millisecond,
	}

	req, err := http.NewRequest("GET", healthURL, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// got a response but it was not an OK
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status code: %d", resp.StatusCode)
	}

	return nil
}

func (p *Process) ProxyRequest(w http.ResponseWriter, r *http.Request) {
	requestBeginTime := time.Now()
	var startDuration time.Duration

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
		beginStartTime := time.Now()
		if err := p.start(); err != nil {
			errstr := fmt.Sprintf("unable to start process: %s", err)
			http.Error(w, errstr, http.StatusBadGateway)
			return
		}
		startDuration = time.Since(beginStartTime)
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

	totalTime := time.Since(requestBeginTime)
	p.proxyLogger.Debugf("Process [%s] request %s - start: %v, total: %v",
		p.ID, r.RequestURI, startDuration, totalTime)
}
