package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mostlygeek/llama-swap/event"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

type ProcessState string

const (
	StateStopped  ProcessState = ProcessState("stopped")
	StateStarting ProcessState = ProcessState("starting")
	StateReady    ProcessState = ProcessState("ready")
	StateStopping ProcessState = ProcessState("stopping")

	// process is shutdown and will not be restarted
	StateShutdown ProcessState = ProcessState("shutdown")
)

type StopStrategy int

const (
	StopImmediately StopStrategy = iota
	StopWaitForInflightRequest
)

type Process struct {
	ID           string
	config       config.ModelConfig
	cmd          *exec.Cmd
	reverseProxy *httputil.ReverseProxy

	// PR #155 called to cancel the upstream process
	cmdMutex       sync.RWMutex
	cancelUpstream context.CancelFunc

	// closed when command exits
	cmdWaitChan chan struct{}

	processLogger *LogMonitor
	proxyLogger   *LogMonitor

	healthCheckTimeout      int
	healthCheckLoopInterval time.Duration

	lastRequestHandledMutex sync.RWMutex
	lastRequestHandled      time.Time

	stateMutex sync.RWMutex
	state      ProcessState

	inFlightRequests      sync.WaitGroup
	inFlightRequestsCount atomic.Int32

	// used to block on multiple start() calls
	waitStarting sync.WaitGroup

	// for managing concurrency limits
	concurrencyLimitSemaphore chan struct{}

	// used for testing to override the default value
	gracefulStopTimeout time.Duration

	// track the number of failed starts
	failedStartCount int
}

func NewProcess(ID string, healthCheckTimeout int, config config.ModelConfig, processLogger *LogMonitor, proxyLogger *LogMonitor) *Process {
	concurrentLimit := 10
	if config.ConcurrencyLimit > 0 {
		concurrentLimit = config.ConcurrencyLimit
	}

	// Setup the reverse proxy.
	proxyURL, err := url.Parse(config.Proxy)
	if err != nil {
		proxyLogger.Errorf("<%s> invalid proxy URL %q: %v", ID, config.Proxy, err)
	}

	var reverseProxy *httputil.ReverseProxy
	if proxyURL != nil {
		reverseProxy = httputil.NewSingleHostReverseProxy(proxyURL)
		reverseProxy.ModifyResponse = func(resp *http.Response) error {
			// prevent nginx from buffering streaming responses (e.g., SSE)
			if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
				resp.Header.Set("X-Accel-Buffering", "no")
			}
			return nil
		}
	}

	return &Process{
		ID:                      ID,
		config:                  config,
		cmd:                     nil,
		reverseProxy:            reverseProxy,
		cancelUpstream:          nil,
		processLogger:           processLogger,
		proxyLogger:             proxyLogger,
		healthCheckTimeout:      healthCheckTimeout,
		healthCheckLoopInterval: 5 * time.Second, /* default, can not be set by user - used for testing */
		state:                   StateStopped,

		// concurrency limit
		concurrencyLimitSemaphore: make(chan struct{}, concurrentLimit),

		// To be removed when migration over exec.CommandContext is complete
		// stop timeout
		gracefulStopTimeout: 10 * time.Second,
		cmdWaitChan:         make(chan struct{}),
	}
}

// LogMonitor returns the log monitor associated with the process.
func (p *Process) LogMonitor() *LogMonitor {
	return p.processLogger
}

// setLastRequestHandled sets the last request handled time in a thread-safe manner.
func (p *Process) setLastRequestHandled(t time.Time) {
	p.lastRequestHandledMutex.Lock()
	defer p.lastRequestHandledMutex.Unlock()
	p.lastRequestHandled = t
}

// getLastRequestHandled gets the last request handled time in a thread-safe manner.
func (p *Process) getLastRequestHandled() time.Time {
	p.lastRequestHandledMutex.RLock()
	defer p.lastRequestHandledMutex.RUnlock()
	return p.lastRequestHandled
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
		p.proxyLogger.Warnf("<%s> swapState() Unexpected current state %s, expected %s", p.ID, p.state, expectedState)
		return p.state, ErrExpectedStateMismatch
	}

	if !isValidTransition(p.state, newState) {
		p.proxyLogger.Warnf("<%s> swapState() Invalid state transition from %s to %s", p.ID, p.state, newState)
		return p.state, ErrInvalidStateTransition
	}

	p.state = newState

	// Atomically increment waitStarting when entering StateStarting
	// This ensures any thread that sees StateStarting will also see the WaitGroup counter incremented
	if newState == StateStarting {
		p.waitStarting.Add(1)
	}

	p.proxyLogger.Debugf("<%s> swapState() State transitioned from %s to %s", p.ID, expectedState, newState)
	event.Emit(ProcessStateChangeEvent{ProcessName: p.ID, NewState: newState, OldState: expectedState})
	return p.state, nil
}

// Helper function to encapsulate transition rules
func isValidTransition(from, to ProcessState) bool {
	switch from {
	case StateStopped:
		return to == StateStarting
	case StateStarting:
		return to == StateReady || to == StateStopping || to == StateStopped
	case StateReady:
		return to == StateStopping
	case StateStopping:
		return to == StateStopped || to == StateShutdown
	case StateShutdown:
		return false // No transitions allowed from these states
	}
	return false
}

func (p *Process) CurrentState() ProcessState {
	p.stateMutex.RLock()
	defer p.stateMutex.RUnlock()
	return p.state
}

// forceState forces the process state to the new state with mutex protection.
// This should only be used in exceptional cases where the normal state transition
// validation via swapState() cannot be used.
func (p *Process) forceState(newState ProcessState) {
	p.stateMutex.Lock()
	defer p.stateMutex.Unlock()
	p.state = newState
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

	// waitStarting.Add(1) is now called atomically in swapState() when transitioning to StateStarting
	defer p.waitStarting.Done()
	cmdContext, ctxCancelUpstream := context.WithCancel(context.Background())

	p.cmd = exec.CommandContext(cmdContext, args[0], args[1:]...)
	p.cmd.Stdout = p.processLogger
	p.cmd.Stderr = p.processLogger
	p.cmd.Env = append(p.cmd.Environ(), p.config.Env...)
	p.cmd.Cancel = p.cmdStopUpstreamProcess
	p.cmd.WaitDelay = p.gracefulStopTimeout

	p.cmdMutex.Lock()
	p.cancelUpstream = ctxCancelUpstream
	p.cmdWaitChan = make(chan struct{})
	p.cmdMutex.Unlock()

	p.failedStartCount++ // this will be reset to zero when the process has successfully started

	p.proxyLogger.Debugf("<%s> Executing start command: %s, env: %s", p.ID, strings.Join(args, " "), strings.Join(p.config.Env, ", "))
	err = p.cmd.Start()

	// Set process state to failed
	if err != nil {
		if curState, swapErr := p.swapState(StateStarting, StateStopped); swapErr != nil {
			p.forceState(StateStopped) // force it into a stopped state
			return fmt.Errorf(
				"failed to start command '%s' and state swap failed. command error: %v, current state: %v, state swap error: %v",
				strings.Join(args, " "), err, curState, swapErr,
			)
		}
		return fmt.Errorf("start() failed for command '%s': %v", strings.Join(args, " "), err)
	}

	// Capture the exit error for later signalling
	go p.waitForCmd()

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
		proxyTo := p.config.Proxy
		healthURL, err := url.JoinPath(proxyTo, checkEndpoint)
		if err != nil {
			return fmt.Errorf("failed to create health check URL proxy=%s and checkEndpoint=%s", proxyTo, checkEndpoint)
		}

		// Ready Check loop
		for {
			currentState := p.CurrentState()
			if currentState != StateStarting {
				if currentState == StateStopped {
					return fmt.Errorf("upstream command exited prematurely but successfully")
				}
				return errors.New("health check interrupted due to shutdown")
			}

			if time.Since(checkStartTime) > maxDuration {
				p.stopCommand()
				return fmt.Errorf("health check timed out after %vs", maxDuration.Seconds())
			}

			if err := p.checkHealthEndpoint(healthURL); err == nil {
				p.proxyLogger.Infof("<%s> Health check passed on %s", p.ID, healthURL)
				break
			} else {
				if strings.Contains(err.Error(), "connection refused") {
					ttl := time.Until(checkStartTime.Add(maxDuration))
					p.proxyLogger.Debugf("<%s> Connection refused on %s, giving up in %.0fs (normal during startup)", p.ID, healthURL, ttl.Seconds())
				} else {
					p.proxyLogger.Debugf("<%s> Health check error on %s, %v (normal during startup)", p.ID, healthURL, err)
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

				// skip the TTL check if there are inflight requests
				if p.inFlightRequestsCount.Load() != 0 {
					continue
				}

				if time.Since(p.getLastRequestHandled()) > maxDuration {
					p.proxyLogger.Infof("<%s> Unloading model, TTL of %ds reached", p.ID, p.config.UnloadAfter)
					p.Stop()
					return
				}
			}
		}()
	}

	if curState, err := p.swapState(StateStarting, StateReady); err != nil {
		return fmt.Errorf("failed to set Process state to ready: current state: %v, error: %v", curState, err)
	} else {
		p.failedStartCount = 0
		return nil
	}
}

// Stop will wait for inflight requests to complete before stopping the process.
func (p *Process) Stop() {
	if !isValidTransition(p.CurrentState(), StateStopping) {
		return
	}

	// wait for any inflight requests before proceeding
	p.proxyLogger.Debugf("<%s> Stop(): Waiting for inflight requests to complete", p.ID)
	p.inFlightRequests.Wait()
	p.StopImmediately()
}

// StopImmediately will transition the process to the stopping state and stop the process with a SIGTERM.
// If the process does not stop within the specified timeout, it will be forcefully stopped with a SIGKILL.
func (p *Process) StopImmediately() {
	if !isValidTransition(p.CurrentState(), StateStopping) {
		return
	}

	p.proxyLogger.Debugf("<%s> Stopping process, current state: %s", p.ID, p.CurrentState())
	if curState, err := p.swapState(StateReady, StateStopping); err != nil {
		p.proxyLogger.Infof("<%s> Stop() Ready -> StateStopping err: %v, current state: %v", p.ID, err, curState)
		return
	}

	p.stopCommand()
}

// Shutdown is called when llama-swap is shutting down. It will give a little bit
// of time for any inflight requests to complete before shutting down. If the Process
// is in the state of starting, it will cancel it and shut it down. Once a process is in
// the StateShutdown state, it can not be started again.
func (p *Process) Shutdown() {
	if !isValidTransition(p.CurrentState(), StateStopping) {
		return
	}

	p.stopCommand()
	// just force it to this state since there is no recovery from shutdown
	p.forceState(StateShutdown)
}

// stopCommand will send a SIGTERM to the process and wait for it to exit.
// If it does not exit within 5 seconds, it will send a SIGKILL.
func (p *Process) stopCommand() {
	stopStartTime := time.Now()
	defer func() {
		p.proxyLogger.Debugf("<%s> stopCommand took %v", p.ID, time.Since(stopStartTime))
	}()

	p.cmdMutex.RLock()
	cancelUpstream := p.cancelUpstream
	cmdWaitChan := p.cmdWaitChan
	p.cmdMutex.RUnlock()

	if cancelUpstream == nil {
		p.proxyLogger.Errorf("<%s> stopCommand has a nil p.cancelUpstream()", p.ID)
		return
	}

	cancelUpstream()
	<-cmdWaitChan
}

func (p *Process) checkHealthEndpoint(healthURL string) error {

	client := &http.Client{
		// wait a short time for a tcp connection to be established
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 500 * time.Millisecond,
			}).DialContext,
		},

		// give a long time to respond to the health check endpoint
		// after the connection is established. See issue: 276
		Timeout: 5000 * time.Millisecond,
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
	if currentState == StateShutdown || currentState == StateStopping {
		http.Error(w, fmt.Sprintf("Process can not ProxyRequest, state is %s", currentState), http.StatusServiceUnavailable)
		return
	}

	select {
	case p.concurrencyLimitSemaphore <- struct{}{}:
		defer func() { <-p.concurrencyLimitSemaphore }()
	default:
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	}

	p.inFlightRequests.Add(1)
	p.inFlightRequestsCount.Add(1)
	defer func() {
		p.setLastRequestHandled(time.Now())
		p.inFlightRequestsCount.Add(-1)
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

	// recover from http.ErrAbortHandler panics that can occur when the client
	// disconnects before the response is sent
	defer func() {
		if r := recover(); r != nil {
			if r == http.ErrAbortHandler {
				p.proxyLogger.Infof("<%s> recovered from client disconnection during streaming", p.ID)
			} else {
				p.proxyLogger.Infof("<%s> recovered from panic: %v", p.ID, r)
			}
		}
	}()

	if p.reverseProxy != nil {
		p.reverseProxy.ServeHTTP(w, r)
	} else {
		http.Error(w, fmt.Sprintf("No reverse proxy available for %s", p.ID), http.StatusInternalServerError)
	}

	totalTime := time.Since(requestBeginTime)
	p.proxyLogger.Debugf("<%s> request %s - start: %v, total: %v",
		p.ID, r.RequestURI, startDuration, totalTime)
}

// waitForCmd waits for the command to exit and handles exit conditions depending on current state
func (p *Process) waitForCmd() {
	exitErr := p.cmd.Wait()
	p.proxyLogger.Debugf("<%s> cmd.Wait() returned error: %v", p.ID, exitErr)

	if exitErr != nil {
		if errno, ok := exitErr.(syscall.Errno); ok {
			p.proxyLogger.Errorf("<%s> errno >> %v", p.ID, errno)
		} else if exitError, ok := exitErr.(*exec.ExitError); ok {
			if strings.Contains(exitError.String(), "signal: terminated") {
				p.proxyLogger.Debugf("<%s> Process stopped OK", p.ID)
			} else if strings.Contains(exitError.String(), "signal: interrupt") {
				p.proxyLogger.Debugf("<%s> Process interrupted OK", p.ID)
			} else {
				p.proxyLogger.Warnf("<%s> ExitError >> %v, exit code: %d", p.ID, exitError, exitError.ExitCode())
			}
		} else {
			if exitErr.Error() != "context canceled" /* this is normal */ {
				p.proxyLogger.Errorf("<%s> Process exited >> %v", p.ID, exitErr)
			}
		}
	}

	currentState := p.CurrentState()
	switch currentState {
	case StateStopping:
		if curState, err := p.swapState(StateStopping, StateStopped); err != nil {
			p.proxyLogger.Errorf("<%s> Process exited but could not swap to StateStopped. curState=%s, err: %v", p.ID, curState, err)
			p.forceState(StateStopped)
		}
	default:
		p.proxyLogger.Infof("<%s> process exited but not StateStopping, current state: %s", p.ID, currentState)
		p.forceState(StateStopped) // force it to be in this state
	}

	p.cmdMutex.Lock()
	close(p.cmdWaitChan)
	p.cmdMutex.Unlock()
}

// cmdStopUpstreamProcess attemps to stop the upstream process gracefully
func (p *Process) cmdStopUpstreamProcess() error {
	p.processLogger.Debugf("<%s> cmdStopUpstreamProcess() initiating graceful stop of upstream process", p.ID)

	// this should never happen ...
	if p.cmd == nil || p.cmd.Process == nil {
		p.proxyLogger.Debugf("<%s> cmd or cmd.Process is nil (normal during config reload)", p.ID)
		return fmt.Errorf("<%s> process is nil or cmd is nil, skipping graceful stop", p.ID)
	}

	if p.config.CmdStop != "" {
		// replace ${PID} with the pid of the process
		stopArgs, err := config.SanitizeCommand(strings.ReplaceAll(p.config.CmdStop, "${PID}", fmt.Sprintf("%d", p.cmd.Process.Pid)))
		if err != nil {
			p.proxyLogger.Errorf("<%s> Failed to sanitize stop command: %v", p.ID, err)
			return err
		}

		p.proxyLogger.Debugf("<%s> Executing stop command: %s", p.ID, strings.Join(stopArgs, " "))

		stopCmd := exec.Command(stopArgs[0], stopArgs[1:]...)
		stopCmd.Stdout = p.processLogger
		stopCmd.Stderr = p.processLogger
		stopCmd.Env = p.cmd.Env

		if err := stopCmd.Run(); err != nil {
			p.proxyLogger.Errorf("<%s> Failed to exec stop command: %v", p.ID, err)
			return err
		}
	} else {
		if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			p.proxyLogger.Errorf("<%s> Failed to send SIGTERM to process: %v", p.ID, err)
			return err
		}
	}

	return nil
}
