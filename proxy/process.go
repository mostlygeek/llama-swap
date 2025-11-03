package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
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

	if p.reverseProxy == nil {
		http.Error(w, fmt.Sprintf("No reverse proxy available for %s", p.ID), http.StatusInternalServerError)
		return
	}

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

	// for #366
	// - extract streaming param from request context, should have been set by proxymanager
	var srw *statusResponseWriter
	swapCtx, cancelLoadCtx := context.WithCancel(r.Context())
	// start the process on demand
	if p.CurrentState() != StateReady {
		// start a goroutine to stream loading status messages into the response writer
		// add a sync so the streaming client only runs when the goroutine has exited

		isStreaming, _ := r.Context().Value(proxyCtxKey("streaming")).(bool)
		if p.config.SendLoadingState != nil && *p.config.SendLoadingState && isStreaming {
			srw = newStatusResponseWriter(p, w)
			go srw.statusUpdates(swapCtx)
		} else {
			p.proxyLogger.Debugf("<%s> SendLoadingState is nil or false, not streaming loading state", p.ID)
		}

		beginStartTime := time.Now()
		if err := p.start(); err != nil {
			errstr := fmt.Sprintf("unable to start process: %s", err)
			cancelLoadCtx()
			if srw != nil {
				srw.sendData(fmt.Sprintf("Unable to swap model err: %s\n", errstr))
				// Wait for statusUpdates goroutine to finish writing its deferred "Done!" messages
				// before closing the connection. Without this, the connection would close before
				// the goroutine can write its cleanup messages, causing incomplete SSE output.
				srw.waitForCompletion(100 * time.Millisecond)
			} else {
				http.Error(w, errstr, http.StatusBadGateway)
			}
			return
		}
		startDuration = time.Since(beginStartTime)
	}

	// should trigger srw to stop sending loading events ...
	cancelLoadCtx()

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

	if srw != nil {
		// Wait for the goroutine to finish writing its final messages
		const completionTimeout = 1 * time.Second
		if !srw.waitForCompletion(completionTimeout) {
			p.proxyLogger.Warnf("<%s> status updates goroutine did not complete within %v, proceeding with proxy request", p.ID, completionTimeout)
		}
		p.reverseProxy.ServeHTTP(srw, r)
	} else {
		p.reverseProxy.ServeHTTP(w, r)
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

var loadingRemarks = []string{
	"Still faster than your last standup meeting...",
	"Reticulating splines...",
	"Waking up the hamsters...",
	"Teaching the model manners...",
	"Convincing the GPU to participate...",
	"Loading weights (they're heavy)...",
	"Herding electrons...",
	"Compiling excuses for the delay...",
	"Downloading more RAM...",
	"Asking the model nicely to boot up...",
	"Bribing CUDA with cookies...",
	"Still loading (blame VRAM)...",
	"The model is fashionably late...",
	"Warming up those tensors...",
	"Making the neural net do push-ups...",
	"Your patience is appreciated (really)...",
	"Almost there (probably)...",
	"Loading like it's 1999...",
	"The model forgot where it put its keys...",
	"Quantum tunneling through layers...",
	"Negotiating with the PCIe bus...",
	"Defrosting frozen parameters...",
	"Teaching attention heads to focus...",
	"Running the matrix (slowly)...",
	"Untangling transformer blocks...",
	"Calibrating the flux capacitor...",
	"Spinning up the probability wheels...",
	"Waiting for the GPU to wake from its nap...",
	"Converting caffeine to compute...",
	"Allocating virtual patience...",
	"Performing arcane CUDA rituals...",
	"The model is stuck in traffic...",
	"Inflating embeddings...",
	"Summoning computational demons...",
	"Pleading with the OOM killer...",
	"Calculating the meaning of life (still at 42)...",
	"Training the training wheels...",
	"Optimizing the optimizer...",
	"Bootstrapping the bootstrapper...",
	"Loading loading screen...",
	"Processing processing logs...",
	"Buffering buffer overflow jokes...",
	"The model hit snooze...",
	"Debugging the debugger...",
	"Compiling the compiler...",
	"Parsing the parser (meta)...",
	"Tokenizing tokens...",
	"Encoding the encoder...",
	"Hashing hash browns...",
	"Forking spoons (not forks)...",
	"The model is contemplating existence...",
	"Transcending dimensional barriers...",
	"Invoking elder tensor gods...",
	"Unfurling probability clouds...",
	"Synchronizing parallel universes...",
	"The GPU is having second thoughts...",
	"Recalibrating reality matrices...",
	"Time is an illusion, loading doubly so...",
	"Convincing bits to flip themselves...",
	"The model is reading its own documentation...",
}

type statusResponseWriter struct {
	hasWritten bool
	writer     http.ResponseWriter
	process    *Process
	wg         sync.WaitGroup // Track goroutine completion
	start      time.Time
}

func newStatusResponseWriter(p *Process, w http.ResponseWriter) *statusResponseWriter {
	s := &statusResponseWriter{
		writer:  w,
		process: p,
		start:   time.Now(),
	}

	s.Header().Set("Content-Type", "text/event-stream") // SSE
	s.Header().Set("Cache-Control", "no-cache")         // no-cache
	s.Header().Set("Connection", "keep-alive")          // keep-alive
	s.WriteHeader(http.StatusOK)                        // send status code 200
	s.sendLine("━━━━━")
	s.sendLine(fmt.Sprintf("llama-swap loading model: %s", p.ID))
	return s
}

// statusUpdates sends status updates to the client while the model is loading
func (s *statusResponseWriter) statusUpdates(ctx context.Context) {
	s.wg.Add(1)
	defer s.wg.Done()

	// Recover from panics caused by client disconnection
	// Note: recover() only works within the same goroutine, so we need it here
	defer func() {
		if r := recover(); r != nil {
			s.process.proxyLogger.Debugf("<%s> statusUpdates recovered from panic (likely client disconnect): %v", s.process.ID, r)
		}
	}()

	defer func() {
		duration := time.Since(s.start)
		s.sendLine(fmt.Sprintf("\nDone! (%.2fs)", duration.Seconds()))
		s.sendLine("━━━━━")
		s.sendLine(" ")
	}()

	// Create a shuffled copy of loadingRemarks
	remarks := make([]string, len(loadingRemarks))
	copy(remarks, loadingRemarks)
	rand.Shuffle(len(remarks), func(i, j int) {
		remarks[i], remarks[j] = remarks[j], remarks[i]
	})
	ri := 0

	// Pick a random duration to send a remark
	nextRemarkIn := time.Duration(2+rand.Intn(4)) * time.Second
	lastRemarkTime := time.Now()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop() // Ensure ticker is stopped to prevent resource leak
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.process.CurrentState() == StateReady {
				return
			}

			// Check if it's time for a snarky remark
			if time.Since(lastRemarkTime) >= nextRemarkIn {
				remark := remarks[ri%len(remarks)]
				ri++
				s.sendLine(fmt.Sprintf("\n%s", remark))
				lastRemarkTime = time.Now()
				// Pick a new random duration for the next remark
				nextRemarkIn = time.Duration(5+rand.Intn(5)) * time.Second
			} else {
				s.sendData(".")
			}
		}
	}
}

// waitForCompletion waits for the statusUpdates goroutine to finish
func (s *statusResponseWriter) waitForCompletion(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (s *statusResponseWriter) sendLine(line string) {
	s.sendData(line + "\n")
}

func (s *statusResponseWriter) sendData(data string) {
	// Create the proper SSE JSON structure
	type Delta struct {
		ReasoningContent string `json:"reasoning_content"`
	}
	type Choice struct {
		Delta Delta `json:"delta"`
	}
	type SSEMessage struct {
		Choices []Choice `json:"choices"`
	}

	msg := SSEMessage{
		Choices: []Choice{
			{
				Delta: Delta{
					ReasoningContent: data,
				},
			},
		},
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		s.process.proxyLogger.Errorf("<%s> Failed to marshal SSE message: %v", s.process.ID, err)
		return
	}

	// Write SSE formatted data, panic if not able to write
	_, err = fmt.Fprintf(s.writer, "data: %s\n\n", jsonData)
	if err != nil {
		panic(fmt.Sprintf("<%s> Failed to write SSE data: %v", s.process.ID, err))
	}
	s.Flush()
}

func (s *statusResponseWriter) Header() http.Header {
	return s.writer.Header()
}

func (s *statusResponseWriter) Write(data []byte) (int, error) {
	return s.writer.Write(data)
}

func (s *statusResponseWriter) WriteHeader(statusCode int) {
	if s.hasWritten {
		return
	}
	s.hasWritten = true
	s.writer.WriteHeader(statusCode)
	s.Flush()
}

// Add Flush method
func (s *statusResponseWriter) Flush() {
	if flusher, ok := s.writer.(http.Flusher); ok {
		flusher.Flush()
	}
}
