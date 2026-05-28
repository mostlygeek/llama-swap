package process

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/shared"
)

var ErrStartAborted = fmt.Errorf("aborted")

type runReq struct {
	timeout time.Duration
	respond chan error
}

type stopReq struct {
	timeout time.Duration
	respond chan error
}

type waitReadyReq struct {
	respond chan error
}

type startResult struct {
	cmd       *exec.Cmd
	cmdDone   chan struct{}
	handlerFn http.HandlerFunc
	err       error
}

type ProcessCommand struct {
	id        string
	config    config.ModelConfig
	parentCtx context.Context

	processLogger *logmon.Monitor
	proxyLogger   *logmon.Monitor

	runCh       chan runReq
	stopCh      chan stopReq
	waitReadyCh chan waitReadyReq

	// current ProcessState. Written only by run(); read by State() via atomic load.
	state atomic.Value

	// stores the active reverse-proxy handler when the process is running.
	// Written only by run(); read by ServeHTTP via atomic load.
	handler atomic.Pointer[http.HandlerFunc]

	lastUse  atomic.Int64 // unix nano timestamp of last ServeHTTP completion
	inflight atomic.Int64 // current in-flight ServeHTTP calls
}

var _ Process = (*ProcessCommand)(nil)

func New(
	parentCtx context.Context,
	id string,
	conf config.ModelConfig,
	processLogger *logmon.Monitor,
	proxyLogger *logmon.Monitor,
) (*ProcessCommand, error) {
	p := &ProcessCommand{
		id:            id,
		config:        conf,
		parentCtx:     parentCtx,
		processLogger: processLogger,
		proxyLogger:   proxyLogger,

		runCh:       make(chan runReq),
		stopCh:      make(chan stopReq),
		waitReadyCh: make(chan waitReadyReq),
	}
	p.state.Store(StateStopped)

	go p.run()
	return p, nil
}

func (p *ProcessCommand) Logger() *logmon.Monitor { return p.processLogger }

// run is the single-writer goroutine that owns all mutable lifecycle state
// (current ProcessState, the running *exec.Cmd, the active reverse-proxy
// handler, and the list of WaitReady subscribers). Every public method
// (Run / Stop / State / WaitReady) is a thin client that sends a request on
// one of the channels below and waits for a response — this funnels concurrent
// callers through a single serialization point so the state machine never
// observes a race.
func (p *ProcessCommand) run() {
	// Mutable state — only read/written from this goroutine. ServeHTTP reads
	// p.handler concurrently, which is why handler is an atomic.Pointer.
	// p.state mirrors `state` so State() can observe transitions; setState
	// writes both.
	state := StateStopped
	setState := func(s ProcessState) {
		old := state
		state = s
		p.state.Store(s)
		if old != s {
			event.Emit(shared.ProcessStateChangeEvent{
				ProcessName: p.id,
				OldState:    string(old),
				NewState:    string(s),
			})
		}
	}
	var (
		cmd          *exec.Cmd
		cmdDone      <-chan struct{}
		readyWaiters []waitReadyReq
		// runResp parks the in-flight Run caller's response channel. The
		// interface contract is that Run blocks until the process is
		// terminated, so we hold this until Stop, parentCtx, or an
		// upstream exit unblocks it via respondRun.
		runResp chan<- error
	)

	// notifyWaiters wakes every blocked WaitReady caller with the given result.
	// Used on transitions out of StateStarting (ready, failed, aborted, or
	// shutdown) — anything that resolves the "is it ready yet?" question.
	notifyWaiters := func(err error) {
		for _, w := range readyWaiters {
			select {
			case w.respond <- err:
			default:
			}
		}
		readyWaiters = nil
	}

	// respondRun delivers the final Run result, if a Run caller is parked.
	respondRun := func(err error) {
		if runResp != nil {
			runResp <- err
			runResp = nil
		}
	}

	for {
		select {
		// Shutdown: parent context cancelled. Tear down any running process,
		// wake any pending WaitReady callers with an error, then exit the
		// goroutine permanently. Subsequent public-method calls will fail
		// because parentCtx.Done() unblocks their send-side selects.
		case <-p.parentCtx.Done():
			// Mark shutdown before killProcess so concurrent State() readers
			// stop treating this process as ready while the (possibly slow)
			// teardown is in progress.
			setState(StateShutdown)
			if cmd != nil {
				p.handler.Store(nil)
				p.killProcess(cmd, cmdDone, 100*time.Millisecond)
				cmd = nil
				cmdDone = nil
			}
			notifyWaiters(fmt.Errorf("[%s] shutdown", p.id))
			respondRun(fmt.Errorf("[%s] shutdown", p.id))
			return

		// Upstream exited on its own (not via Stop). Drop handler state,
		// transition to Stopped, and unblock the parked Run caller.
		// cmdDone is nil while no process is running, so this case is
		// dormant outside of StateReady.
		case <-cmdDone:
			cmd = nil
			cmdDone = nil
			p.handler.Store(nil)
			setState(StateStopped)
			respondRun(fmt.Errorf("[%s] upstream exited unexpectedly", p.id))

		// WaitReady: if we're already in a terminal-for-this-question state,
		// respond immediately; otherwise queue the caller and let a future
		// state transition wake them via notifyWaiters.
		case req := <-p.waitReadyCh:
			switch state {
			case StateReady:
				req.respond <- nil
			case StateShutdown:
				req.respond <- fmt.Errorf("[%s] shutdown", p.id)
			default:
				readyWaiters = append(readyWaiters, req)
			}

		// Run: start the upstream process. Only valid from StateStopped.
		// doStart can take a long time (health-check polling), so it runs in
		// a separate goroutine and we wait on resultCh. While waiting we also
		// listen for an incoming Stop — that's how callers cancel an in-flight
		// start.
		case req := <-p.runCh:
			if state != StateStopped {
				req.respond <- fmt.Errorf("[%s] could not be started in %s state", p.id, state)
				continue
			}
			setState(StateStarting)

			startCtx, cancelStart := context.WithCancel(context.Background())
			resultCh := make(chan startResult, 1)
			go func() {
				resultCh <- p.doStart(startCtx, req.timeout)
			}()

			// pendingStop holds a Stop request that arrived mid-start, so we
			// can respond to it AFTER we've finished tearing the start down.
			var pendingStop *stopReq
			select {
			// doStart finished on its own — either successfully (latch
			// cmd/handler and move to Ready) or with an error (back to
			// Stopped). Either way wake WaitReady subscribers and reply
			// to the Run caller.
			case res := <-resultCh:
				if res.err == nil {
					cmd = res.cmd
					cmdDone = res.cmdDone
					fn := res.handlerFn
					p.handler.Store(&fn)
					setState(StateReady)
					notifyWaiters(nil)
					// Park the Run response — Run blocks until the process
					// terminates, so we only fire this when Stop, parentCtx,
					// or the upstream exit takes the process down.
					runResp = req.respond

					// Start TTL goroutine if configured — self-terminates
					// when state leaves StateReady.
					if p.config.UnloadAfter > 0 {
						ttlDuration := time.Duration(p.config.UnloadAfter) * time.Second
						go func() {
							ticker := time.NewTicker(time.Second)
							defer ticker.Stop()
							for range ticker.C {
								if p.State() != StateReady {
									return
								}
								if p.inflight.Load() != 0 {
									continue
								}
								if time.Since(time.Unix(0, p.lastUse.Load())) > ttlDuration {
									p.proxyLogger.Infof("<%s> Unloading model, TTL of %ds reached", p.id, p.config.UnloadAfter)
									p.Stop(10 * time.Second)
									return
								}
							}
						}()
					}
				} else {
					setState(StateStopped)
					notifyWaiters(res.err)
					req.respond <- res.err
				}

			// Stop arrived while doStart was still running. Cancel the
			// start context to abort it, then wait for doStart to return.
			// If doStart had already crossed the finish line before
			// cancellation took effect, it returns a live cmd that we
			// must kill ourselves. The Run caller gets ErrAbort; the Stop
			// caller is parked in pendingStop and answered below.
			case stop := <-p.stopCh:
				cancelStart()
				res := <-resultCh
				if res.cmd != nil {
					p.killProcess(res.cmd, res.cmdDone, stop.timeout)
				}
				setState(StateStopped)
				notifyWaiters(ErrStartAborted)
				req.respond <- ErrStartAborted
				pendingStop = &stop

			// Parent context cancelled (e.g. config reload) while doStart
			// was still running. Stop() returns early when parentCtx is
			// done and never sends on stopCh, so we must handle shutdown
			// here to avoid leaving doStart running indefinitely.
			case <-p.parentCtx.Done():
				cancelStart()
				// Mark shutdown before tearing the process down: killProcess
				// may block (e.g. taskkill on Windows is slow to spawn), and
				// callers observing State() should see StateShutdown promptly
				// rather than a stale StateStarting.
				setState(StateShutdown)
				res := <-resultCh
				if res.cmd != nil {
					p.killProcess(res.cmd, res.cmdDone, 100*time.Millisecond)
				}
				notifyWaiters(fmt.Errorf("[%s] shutdown", p.id))
				respondRun(fmt.Errorf("[%s] shutdown", p.id))
				return
			}
			// cancelStart is idempotent; calling it again here ensures the
			// context is released even on the success path (govet leak check).
			cancelStart()
			if pendingStop != nil {
				pendingStop.respond <- nil
			}

		// Stop: tear down a running process.
		case stop := <-p.stopCh:
			if cmd != nil {
				setState(StateStopping)
				p.killProcess(cmd, cmdDone, stop.timeout)
				cmd = nil
				cmdDone = nil
				p.handler.Store(nil)
			}
			// Stop is a no-op (and not an error) when already Stopped — this
			// is what makes it idempotent for callers that don't track state.
			setState(StateStopped)
			respondRun(nil)
			stop.respond <- nil
		}
	}
}

func (p *ProcessCommand) doStart(startCtx context.Context, healthCheckTimeout time.Duration) startResult {
	if p.config.Proxy == "" {
		return startResult{err: fmt.Errorf("upstream proxy missing")}
	}

	args, err := p.config.SanitizedCommand()
	if err != nil {
		return startResult{err: fmt.Errorf("unable to get sanitized command: %w", err)}
	}

	proxyURL, err := url.Parse(p.config.Proxy)
	if err != nil {
		return startResult{err: fmt.Errorf("invalid proxy URL %q: %w", p.config.Proxy, err)}
	}

	reverseProxy := httputil.NewSingleHostReverseProxy(proxyURL)
	reverseProxy.Transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   time.Duration(p.config.Timeouts.Connect) * time.Second,
			KeepAlive: time.Duration(p.config.Timeouts.KeepAlive) * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   time.Duration(p.config.Timeouts.TLSHandshake) * time.Second,
		ResponseHeaderTimeout: time.Duration(p.config.Timeouts.ResponseHeader) * time.Second,
		ExpectContinueTimeout: time.Duration(p.config.Timeouts.ExpectContinue) * time.Second,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       time.Duration(p.config.Timeouts.IdleConn) * time.Second,
	}
	reverseProxy.ModifyResponse = func(resp *http.Response) error {
		if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
			resp.Header.Set("X-Accel-Buffering", "no")
		}
		return nil
	}
	// httputil.ReverseProxy panics with http.ErrAbortHandler when the upstream
	// disconnects after response headers have been sent. Recover here so the
	// streaming termination is treated as a normal client/upstream disconnect.
	// see: https://github.com/golang/go/issues/23643
	handlerFn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					p.proxyLogger.Infof("<%s> recovered from upstream disconnection during streaming", p.id)
				} else {
					p.proxyLogger.Warnf("<%s> recovered from panic: %v", p.id, rec)
				}
			}
		}()
		reverseProxy.ServeHTTP(w, r)
	})

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stderr = p.processLogger
	cmd.Stdout = p.processLogger
	cmd.Env = append(cmd.Environ(), p.config.Env...)
	setProcAttributes(cmd)

	p.proxyLogger.Debugf("<%s> Executing start command: %s, env: %s", p.id, strings.Join(args, " "), strings.Join(p.config.Env, ", "))

	cmdDone := make(chan struct{})
	if err := cmd.Start(); err != nil {
		return startResult{err: fmt.Errorf("failed to start command '%s': %w", strings.Join(args, " "), err)}
	}

	go func() {
		waitErr := cmd.Wait()
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			p.proxyLogger.Debugf("<%s> process exited: code=%d, err=%v", p.id, exitErr.ExitCode(), waitErr)
		} else if waitErr != nil {
			p.proxyLogger.Debugf("<%s> process exited with error: %v", p.id, waitErr)
		} else {
			p.proxyLogger.Debugf("<%s> process exited cleanly", p.id)
		}
		close(cmdDone)
	}()

	if startCtx.Err() != nil {
		p.killProcess(cmd, cmdDone, 5*time.Second)
		return startResult{err: ErrStartAborted}
	}

	checkEndpoint := strings.TrimSpace(p.config.CheckEndpoint)
	if checkEndpoint == "none" {
		return startResult{cmd: cmd, cmdDone: cmdDone, handlerFn: handlerFn}
	}

	// Wait 250ms for the command to start up before health checking
	select {
	case <-startCtx.Done():
		p.killProcess(cmd, cmdDone, 5*time.Second)
		return startResult{err: ErrStartAborted}
	case <-time.After(250 * time.Millisecond):
	}

	deadline := time.Now().Add(healthCheckTimeout)
	for {
		select {
		case <-startCtx.Done():
			p.killProcess(cmd, cmdDone, 5*time.Second)
			return startResult{err: ErrStartAborted}
		case <-cmdDone:
			return startResult{err: fmt.Errorf("upstream command exited prematurely")}
		default:
		}

		if time.Now().After(deadline) {
			p.killProcess(cmd, cmdDone, 5*time.Second)
			return startResult{err: fmt.Errorf("health check timed out after %v", healthCheckTimeout)}
		}

		req, _ := http.NewRequestWithContext(startCtx, "GET", p.config.CheckEndpoint, nil)
		rr := httptest.NewRecorder()
		reverseProxy.ServeHTTP(rr, req)
		resp := rr.Result()
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			p.proxyLogger.Infof("<%s> Health check passed on %s%s", p.id, p.config.Proxy, p.config.CheckEndpoint)
			break
		} else if startCtx.Err() != nil {
			p.killProcess(cmd, cmdDone, 5*time.Second)
			return startResult{err: ErrStartAborted}
		}

		select {
		case <-startCtx.Done():
			p.killProcess(cmd, cmdDone, 5*time.Second)
			return startResult{err: ErrStartAborted}
		case <-cmdDone:
			return startResult{err: fmt.Errorf("upstream command exited prematurely")}
		case <-time.After(time.Second):
		}
	}

	return startResult{cmd: cmd, cmdDone: cmdDone, handlerFn: handlerFn}
}

func (p *ProcessCommand) killProcess(cmd *exec.Cmd, cmdDone <-chan struct{}, gracefulTimeout time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	if p.config.CmdStop != "" {
		stopArgs, err := config.SanitizeCommand(
			strings.ReplaceAll(p.config.CmdStop, "${PID}", fmt.Sprintf("%d", cmd.Process.Pid)),
		)
		if err == nil {
			stopCmd := exec.Command(stopArgs[0], stopArgs[1:]...)
			stopCmd.Env = cmd.Env
			setProcAttributes(stopCmd)
			stopCmd.Run()
		} else {
			cmd.Process.Signal(syscall.SIGTERM)
		}
	} else {
		cmd.Process.Signal(syscall.SIGTERM)
	}

	timer := time.NewTimer(gracefulTimeout)
	defer timer.Stop()

	select {
	case <-cmdDone:
	case <-timer.C:
		cmd.Process.Kill()
		<-cmdDone
	}
}

func (p *ProcessCommand) ID() string {
	return p.id
}

func (p *ProcessCommand) Run(timeout time.Duration) error {
	req := runReq{
		timeout: timeout,
		respond: make(chan error, 1),
	}
	select {
	case p.runCh <- req:
	case <-p.parentCtx.Done():
		return fmt.Errorf("[%s] shutdown", p.id)
	}
	select {
	case err := <-req.respond:
		return err
	case <-p.parentCtx.Done():
		return fmt.Errorf("[%s] shutdown", p.id)
	}
}

func (p *ProcessCommand) WaitReady(ctx context.Context) error {
	req := waitReadyReq{respond: make(chan error, 1)}
	select {
	case p.waitReadyCh <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-p.parentCtx.Done():
		return fmt.Errorf("[%s] shutdown", p.id)
	}
	select {
	case err := <-req.respond:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *ProcessCommand) Stop(timeout time.Duration) error {
	req := stopReq{
		timeout: timeout,
		respond: make(chan error, 1),
	}
	select {
	case p.stopCh <- req:
	case <-p.parentCtx.Done():
		return fmt.Errorf("[%s] shutdown", p.id)
	}
	return <-req.respond
}

func (p *ProcessCommand) State() ProcessState {
	if s, ok := p.state.Load().(ProcessState); ok {
		return s
	}
	return StateStopped
}

func (p *ProcessCommand) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fn := p.handler.Load()
	if fn == nil {
		http.Error(w, fmt.Sprintf("llama-swap-error: [%s] process is not ready", p.id), http.StatusServiceUnavailable)
		return
	}
	p.inflight.Add(1)
	defer func() {
		p.lastUse.Store(time.Now().UnixNano())
		p.inflight.Add(-1)
	}()
	(*fn)(w, r)
}
