package process

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
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/proxy/config"
	"golang.org/x/sync/semaphore"
)

type basecmd struct {
	ctx context.Context
	msg string
	// the channel to send the response back to
	respond chan error
}

func (c *basecmd) send(err error) {
	if c.respond == nil {
		return
	}

	select {
	case c.respond <- err:
	default:
	}
}

type stopcmd struct {
	basecmd
	immediate bool
}

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

type Process struct {
	id        string
	config    config.ModelConfig
	parentCtx context.Context
	cmd       *exec.Cmd

	// createTestHandler allows injection of a custom http handler function
	// to be used for testing
	createTestHandler func() (http.HandlerFunc, error)
	handlerFn         http.HandlerFunc

	processLogger *logmon.Monitor
	proxyLogger   *logmon.Monitor

	inFlightRequests      sync.WaitGroup
	inFlightRequestsCount atomic.Int32

	sem *semaphore.Weighted

	state ProcessState

	// command channels
	startCh chan basecmd
	readyCh chan basecmd
	stopCh  chan stopcmd
}

func New(
	parentCtx context.Context,
	id string,
	conf config.ModelConfig,
	processLogger *logmon.Monitor,
	proxyLogger *logmon.Monitor,
) (*Process, error) {
	concurrentLimit := 10
	if conf.ConcurrencyLimit > 0 {
		concurrentLimit = conf.ConcurrencyLimit
	}

	process := &Process{
		id:            id,
		config:        conf,
		parentCtx:     parentCtx,
		cmd:           nil,
		processLogger: processLogger,
		proxyLogger:   proxyLogger,
		sem:           semaphore.NewWeighted(int64(concurrentLimit)),
		state:         StateStopped,

		startCh: make(chan basecmd),
		readyCh: make(chan basecmd),
		stopCh:  make(chan stopcmd),
	}

	go process.run()
	return process, nil
}

func (p *Process) createReverseProxyHandlerFunc() (http.HandlerFunc, error) {
	proxyURL, err := url.Parse(p.config.Proxy)
	if err != nil {
		return nil, fmt.Errorf("[%s] invalid proxy URL %q: %v", p.id, p.config.Proxy, err)
	}

	reverseProxy := httputil.NewSingleHostReverseProxy(proxyURL)

	// Create custom transport with configured timeouts
	transport := &http.Transport{
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
	reverseProxy.Transport = transport

	reverseProxy.ModifyResponse = func(resp *http.Response) error {
		// prevent nginx from buffering streaming responses (e.g., SSE)
		if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
			resp.Header.Set("X-Accel-Buffering", "no")
		}
		return nil
	}

	return reverseProxy.ServeHTTP, nil
}

func (p *Process) run() {

	// maintain the cmd context
	cmdCtx, cancelCmd := context.WithCancel(context.Background())

	for {
		select {
		case <-p.parentCtx.Done():
			err := p.stop(cancelCmd, true)
			if err != nil {
				p.proxyLogger.Errorf("[%s] stopped parentCtx.Done error: %v", p.id, err)
			} else {
				p.proxyLogger.Infof("[%s] stopped by parentCtx.Done", p.id)
			}
			return

		// start up the process
		case cmd := <-p.startCh:
			// start the process
			if p.state == StateStopped {
				p.state = StateStarting
				cmdCtx, cancelCmd = context.WithCancel(cmd.ctx)
				err := p.start(cmdCtx)
				if err != nil {
					p.state = StateStopped
				} else {
					p.state = StateReady
				}
				cmd.send(err)
			} else {
				cmd.send(fmt.Errorf("[%s] could not be started in %s state", p.id, p.state))
			}

		// stop the process
		case cmd := <-p.stopCh:
			if p.state == StateReady || p.state == StateStarting {
				p.state = StateStopping
				p.stop(cancelCmd, cmd.immediate)
				p.cmd = nil
				p.state = StateStopped
				cmd.send(nil)
			} else {
				cmd.send(fmt.Errorf("[%s] could not be stopped in %s state", p.id, p.state))
			}
		}
	}
}

// start blocks until the process is started or an error occurs
func (p *Process) start(startCtx context.Context) error {
	if p.cmd != nil {
		return fmt.Errorf("[%s] cmd already exists", p.id)
	}

	// create a new command
	// call .Start()
	// in a goroutine .Wait(), if there is an error send it to the

	if p.config.Proxy == "" {
		return fmt.Errorf("can not start(), upstream proxy missing")
	}

	args, err := p.config.SanitizedCommand()
	if err != nil {
		return fmt.Errorf("unable to get sanitized command: %w", err)
	}

	if p.createTestHandler != nil {
		handler, err := p.createTestHandler()
		if err != nil {
			return fmt.Errorf("[%s] failed to create custom handler: %w", p.id, err)
		} else {
			p.handlerFn = handler
		}
	} else {
		handler, err := p.createReverseProxyHandlerFunc()
		if err != nil {
			return fmt.Errorf("[%s] failed to create default reverse proxy handler: %w", p.id, err)
		} else {
			p.handlerFn = handler
		}

		cmd := exec.CommandContext(startCtx, args[0], args[1:]...)
		cmd.Stdout = p.processLogger
		cmd.Stderr = p.processLogger
		cmd.Env = append(cmd.Environ(), p.config.Env...)
		setProcAttributes(cmd)

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("[%s] start() failed for command '%s': %w", p.id, strings.Join(args, " "), err)
		} else {
			p.cmd = cmd
		}

		// TODO: need a go func for cmd.Wait() ... and when it exits it will send an unexpected
		// exit error to the process loop, which will update the state to stopped
	}

	checkEndpoint := strings.TrimSpace(p.config.CheckEndpoint)

	// a "none" means don't check for health ... I could have picked a better word :facepalm:
	if checkEndpoint != "none" {
		proxyTo := p.config.Proxy
		checkEndpoint := strings.TrimSpace(p.config.CheckEndpoint)
		healthURL, err := url.JoinPath(proxyTo, checkEndpoint)
		if err != nil {
			return fmt.Errorf("failed to create health check URL proxy=%s and checkEndpoint=%s", proxyTo, checkEndpoint)
		}

		if p.createTestHandler == nil {
			// give real processes a bit of time to start
			time.Sleep(250 * time.Millisecond)
		}

		healthCh := make(chan error, 1)
		maxDuration := time.Second * time.Duration(p.config.HealthCheckTimeout)
		checkCtx, _ := context.WithTimeout(startCtx, maxDuration)
		go func() {
			for {
				select {
				case <-checkCtx.Done():
					healthCh <- startCtx.Err()
				default:
					if err := p.checkhealth(healthURL); err != healthcheckFailedError {
						healthCh <- err // nil or some other error
					}
				}
			}
		}()

		err = <-healthCh
		if err != nil {
			return fmt.Errorf("[%s] health check failed: %w", p.id, err)
		}
	}

	return nil
}

func (p *Process) stop(cancelFn context.CancelFunc, immedate bool) error {
	cancelFn()
	p.cmd = nil
	return nil
}

func (p *Process) ID() string {
	return p.id
}

func (p *Process) Start(ctx context.Context) error {
	cmd := basecmd{
		ctx:     ctx,
		respond: make(chan error, 1),
	}
	p.startCh <- cmd
	return <-cmd.respond
}

func (p *Process) Stop(ctx context.Context) error {
	cmd := stopcmd{
		basecmd: basecmd{
			ctx:     ctx,
			respond: make(chan error, 1),
		},
		immediate: false,
	}
	p.stopCh <- cmd
	return <-cmd.respond
}

func (p *Process) StopImmediate(ctx context.Context) error {

	// send a stop immediate message to the command channel

	return nil
}

func (p *Process) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	// in here we can have a race condition where the process
	// is stopping or whatever. ask for a request handler
	// and then execute it. It can still error but we
	// want to see what happens when it races with a stop
	// in run() it can send back a handler function that returns a
	// an error

	if p.handlerFn == nil {
		http.Error(w, fmt.Sprintf("[%s] no handler function available", p.id), http.StatusInternalServerError)
		return
	}

	p.inFlightRequestsCount.Add(1)
	defer p.inFlightRequests.Add(-1)
	p.sem.Acquire(r.Context(), 1)
	defer p.sem.Release(1)
	p.handlerFn(w, r)
}

var healthcheckFailedError = errors.New("healthcheck status not OK")

func (p *Process) checkhealth(checkurl string) error {
	req, err := http.NewRequest("GET", checkurl, nil)
	if err != nil {
		return fmt.Errorf("checkhealth failed to create request: %w", err)
	}

	resp, err := checkclient.Do(req)
	if err != nil {
		return fmt.Errorf("checkhealth failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// got a response but it was not an OK
	if resp.StatusCode != http.StatusOK {
		return healthcheckFailedError
	}

	return nil
}
