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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/proxy/config"
	"golang.org/x/sync/semaphore"
)

var ErrAbort = fmt.Errorf("aborted")

type ProcessState string

const (
	StateStopped  ProcessState = ProcessState("stopped")
	StateStarting ProcessState = ProcessState("starting")
	StateReady    ProcessState = ProcessState("ready")
	StateCooldown ProcessState = ProcessState("cooldown")
	StateStopping ProcessState = ProcessState("stopping")

	// process is shutdown and will not be restarted
	StateShutdown ProcessState = ProcessState("shutdown")
)

type Process interface {
	Run(timeout time.Duration) error
	WaitReady(context.Context) error
	Stop(cooldown time.Duration, timeout time.Duration) error
	State() ProcessState
}

type runReq struct {
	timeout time.Duration
	respond chan error
}

type stopReq struct {
	cooldown time.Duration
	timeout  time.Duration
	respond  chan error
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

	inFlightRequests      sync.WaitGroup
	inFlightRequestsCount atomic.Int32
	sem                   *semaphore.Weighted

	runCh       chan runReq
	stopCh      chan stopReq
	waitReadyCh chan waitReadyReq

	// current ProcessState. Written only by run(); read by State() via atomic load.
	state atomic.Value

	// stores the active reverse-proxy handler when the process is running.
	// Written only by run(); read by ServeHTTP via atomic load.
	handler atomic.Pointer[http.HandlerFunc]
}

var _ Process = (*ProcessCommand)(nil)

func New(
	parentCtx context.Context,
	id string,
	conf config.ModelConfig,
	processLogger *logmon.Monitor,
	proxyLogger *logmon.Monitor,
) (*ProcessCommand, error) {
	concurrentLimit := 10
	if conf.ConcurrencyLimit > 0 {
		concurrentLimit = conf.ConcurrencyLimit
	}

	p := &ProcessCommand{
		id:            id,
		config:        conf,
		parentCtx:     parentCtx,
		processLogger: processLogger,
		proxyLogger:   proxyLogger,
		sem:           semaphore.NewWeighted(int64(concurrentLimit)),

		runCh:       make(chan runReq),
		stopCh:      make(chan stopReq),
		waitReadyCh: make(chan waitReadyReq),
	}
	p.state.Store(StateStopped)

	go p.run()
	return p, nil
}

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
	state := StateStopped
	var (
		cmd          *exec.Cmd
		cmdDone      <-chan struct{}
		readyWaiters []waitReadyReq
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

	for {
		select {
		// Shutdown: parent context cancelled. Tear down any running process,
		// wake any pending WaitReady callers with an error, then exit the
		// goroutine permanently. Subsequent public-method calls will fail
		// because parentCtx.Done() unblocks their send-side selects.
		case <-p.parentCtx.Done():
			if cmd != nil {
				p.killProcess(cmd, cmdDone, 100*time.Millisecond)
				cmd = nil
				cmdDone = nil
				p.handler.Store(nil)
			}
			state = StateShutdown
			notifyWaiters(fmt.Errorf("[%s] shutdown", p.id))
			return

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
			state = StateStarting

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
					state = StateReady
					notifyWaiters(nil)
				} else {
					state = StateStopped
					notifyWaiters(res.err)
				}
				req.respond <- res.err

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
				state = StateStopped
				notifyWaiters(ErrAbort)
				req.respond <- ErrAbort
				pendingStop = &stop
			}
			// cancelStart is idempotent; calling it again here ensures the
			// context is released even on the success path (govet leak check).
			cancelStart()
			if pendingStop != nil {
				pendingStop.respond <- nil
			}

		// Stop: tear down a running process. If cooldown > 0, hold the
		// process in StateCooldown for that duration before killing — used
		// to let in-flight requests drain. The cooldown wait is inline,
		// so other channels (Run, State, WaitReady) won't be serviced
		// during it; queued requests resume after Stop completes.
		case stop := <-p.stopCh:
			if cmd != nil {
				if stop.cooldown > 0 {
					state = StateCooldown
					timer := time.NewTimer(stop.cooldown)
					select {
					case <-timer.C:
					case <-p.parentCtx.Done():
					}
					timer.Stop()
				}
				state = StateStopping
				p.killProcess(cmd, cmdDone, stop.timeout)
				cmd = nil
				cmdDone = nil
				p.handler.Store(nil)
			}
			// Stop is a no-op (and not an error) when already Stopped — this
			// is what makes it idempotent for callers that don't track state.
			state = StateStopped
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
	handlerFn := http.HandlerFunc(reverseProxy.ServeHTTP)

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stderr = p.processLogger
	cmd.Stdout = p.processLogger
	cmd.Env = append(cmd.Environ(), p.config.Env...)
	setProcAttributes(cmd)

	cmdDone := make(chan struct{})
	if err := cmd.Start(); err != nil {
		return startResult{err: fmt.Errorf("failed to start command '%s': %w", strings.Join(args, " "), err)}
	}

	go func() {
		cmd.Wait()
		close(cmdDone)
	}()

	if startCtx.Err() != nil {
		p.killProcess(cmd, cmdDone, 5*time.Second)
		return startResult{err: ErrAbort}
	}

	checkEndpoint := strings.TrimSpace(p.config.CheckEndpoint)
	if checkEndpoint == "none" {
		return startResult{cmd: cmd, cmdDone: cmdDone, handlerFn: handlerFn}
	}

	select {
	case <-startCtx.Done():
		p.killProcess(cmd, cmdDone, 5*time.Second)
		return startResult{err: ErrAbort}
	case <-time.After(250 * time.Millisecond):
	}

	deadline := time.Now().Add(healthCheckTimeout)
	for {
		select {
		case <-startCtx.Done():
			p.killProcess(cmd, cmdDone, 5*time.Second)
			return startResult{err: ErrAbort}
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
			break
		} else if startCtx.Err() != nil {
			p.killProcess(cmd, cmdDone, 5*time.Second)
			return startResult{err: ErrAbort}
		}

		select {
		case <-startCtx.Done():
			p.killProcess(cmd, cmdDone, 5*time.Second)
			return startResult{err: ErrAbort}
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
	return <-req.respond
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

func (p *ProcessCommand) Stop(cooldown time.Duration, timeout time.Duration) error {
	req := stopReq{
		cooldown: cooldown,
		timeout:  timeout,
		respond:  make(chan error, 1),
	}
	select {
	case p.stopCh <- req:
	case <-p.parentCtx.Done():
		return fmt.Errorf("[%s] shutdown", p.id)
	}
	return <-req.respond
}

func (p *ProcessCommand) State() ProcessState {
	if s, ok := p.state.Load().(*ProcessState); ok {
		return *s
	}
	return StateStopped
}

func (p *ProcessCommand) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.inFlightRequests.Add(1)
	p.inFlightRequestsCount.Add(1)
	defer p.inFlightRequests.Done()
	p.sem.Acquire(r.Context(), 1)
	defer p.sem.Release(1)

	fn := p.handler.Load()
	if fn == nil {
		http.Error(w, "no handler available", http.StatusInternalServerError)
		return
	}
	(*fn)(w, r)
}
