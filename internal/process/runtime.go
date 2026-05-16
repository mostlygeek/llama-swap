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

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

// shared httpclient for checking upstream runtimes
var checkclient = &http.Client{
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

var ErrAbort = fmt.Errorf("aborted")

type Runtime interface {
	// Start is a synchronous operation that will block until the runtime is ready to
	// receive HTTP traffic
	Start(time.Duration) error

	// Stop is a synchronous operation that will block until the runtime
	// until the runtime will no longer accept HTTP traffic
	Stop(time.Duration) error
	ServeHTTP(http.ResponseWriter, *http.Request)
}

type startRuntimeReq struct {
	timeout time.Duration
	respond chan error
}

type stopRuntimeReq struct {
	timeout time.Duration
	respond chan error
}

type startRuntimeResult struct {
	cmd       *exec.Cmd
	cmdDone   chan struct{}
	handlerFn http.HandlerFunc
	err       error
}

type CommandRuntime struct {
	config  config.ModelConfig
	startCh chan startRuntimeReq
	stopCh  chan stopRuntimeReq
	handler atomic.Pointer[http.HandlerFunc]
	log     *logmon.Monitor
}

func NewCommandRuntime(conf config.ModelConfig, logger *logmon.Monitor) (Runtime, error) {
	c := &CommandRuntime{
		config:  conf,
		startCh: make(chan startRuntimeReq),
		stopCh:  make(chan stopRuntimeReq),
		log:     logger,
	}
	go c.run()
	return c, nil
}

func (r *CommandRuntime) run() {
	var (
		cmd     *exec.Cmd
		cmdDone <-chan struct{}
	)

	for {
		select {
		case req := <-r.startCh:
			if cmd != nil {
				req.respond <- fmt.Errorf("already starting or started")
				continue
			}

			startCtx, cancelStart := context.WithCancel(context.Background())
			startResultCh := make(chan startRuntimeResult, 1)
			go func() {
				startResultCh <- r.doStart(startCtx, req.timeout)
			}()

			// Wait for start to finish or a stop to interrupt it.
			var pendingStopRespond chan error
		waitLoop:
			for {
				select {
				case res := <-startResultCh:
					if res.err == nil {
						cmd = res.cmd
						cmdDone = res.cmdDone
						fn := res.handlerFn
						r.handler.Store(&fn)
					}
					req.respond <- res.err
					break waitLoop

				case stop := <-r.stopCh:
					cancelStart()
					res := <-startResultCh // wait for doStart to abort
					if res.cmd != nil {
						// start completed before cancel took effect; kill it now
						r.killProcess(res.cmd, res.cmdDone, stop.timeout)
					}
					pendingStopRespond = stop.respond

					req.respond <- ErrAbort
					break waitLoop
				}
			}

			cancelStart()
			if pendingStopRespond != nil {
				pendingStopRespond <- nil
			}

		case req := <-r.stopCh:
			if cmd != nil {
				r.killProcess(cmd, cmdDone, req.timeout)
				cmd = nil
				cmdDone = nil
				r.handler.Store(nil)
			}
			req.respond <- nil
		}
	}
}

func (r *CommandRuntime) doStart(startCtx context.Context, healthCheckTimeout time.Duration) startRuntimeResult {
	if r.config.Proxy == "" {
		return startRuntimeResult{err: fmt.Errorf("upstream proxy missing")}
	}

	args, err := r.config.SanitizedCommand()
	if err != nil {
		return startRuntimeResult{err: fmt.Errorf("unable to get sanitized command: %w", err)}
	}

	proxyURL, err := url.Parse(r.config.Proxy)
	if err != nil {
		return startRuntimeResult{err: fmt.Errorf("invalid proxy URL %q: %w", r.config.Proxy, err)}
	}

	reverseProxy := httputil.NewSingleHostReverseProxy(proxyURL)
	reverseProxy.Transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   time.Duration(r.config.Timeouts.Connect) * time.Second,
			KeepAlive: time.Duration(r.config.Timeouts.KeepAlive) * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   time.Duration(r.config.Timeouts.TLSHandshake) * time.Second,
		ResponseHeaderTimeout: time.Duration(r.config.Timeouts.ResponseHeader) * time.Second,
		ExpectContinueTimeout: time.Duration(r.config.Timeouts.ExpectContinue) * time.Second,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       time.Duration(r.config.Timeouts.IdleConn) * time.Second,
	}
	reverseProxy.ModifyResponse = func(resp *http.Response) error {
		if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
			resp.Header.Set("X-Accel-Buffering", "no")
		}
		return nil
	}
	handlerFn := http.HandlerFunc(reverseProxy.ServeHTTP)

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stderr = r.log
	cmd.Stdout = r.log
	cmd.Env = append(cmd.Environ(), r.config.Env...)
	setProcAttributes(cmd)

	cmdDone := make(chan struct{})
	if err := cmd.Start(); err != nil {
		return startRuntimeResult{err: fmt.Errorf("failed to start command '%s': %w", strings.Join(args, " "), err)}
	}

	go func() {
		cmd.Wait()
		close(cmdDone)
	}()

	if startCtx.Err() != nil {
		r.killProcess(cmd, cmdDone, 5*time.Second)
		return startRuntimeResult{err: ErrAbort}
	}

	checkEndpoint := strings.TrimSpace(r.config.CheckEndpoint)
	if checkEndpoint == "none" {
		return startRuntimeResult{cmd: cmd, cmdDone: cmdDone, handlerFn: handlerFn}
	}

	select {
	case <-startCtx.Done():
		r.killProcess(cmd, cmdDone, 5*time.Second)
		return startRuntimeResult{err: ErrAbort}
	case <-time.After(250 * time.Millisecond):
	}

	deadline := time.Now().Add(healthCheckTimeout)
	for {
		select {
		case <-startCtx.Done():
			r.killProcess(cmd, cmdDone, 5*time.Second)
			return startRuntimeResult{err: ErrAbort}
		case <-cmdDone:
			return startRuntimeResult{err: fmt.Errorf("upstream command exited prematurely")}
		default:
		}

		if time.Now().After(deadline) {
			r.killProcess(cmd, cmdDone, 5*time.Second)
			return startRuntimeResult{err: fmt.Errorf("health check timed out after %v", healthCheckTimeout)}
		}

		req, _ := http.NewRequestWithContext(startCtx, "GET", r.config.CheckEndpoint, nil)
		rr := httptest.NewRecorder()
		reverseProxy.ServeHTTP(rr, req)
		resp := rr.Result()
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			break
		} else if startCtx.Err() != nil {
			r.killProcess(cmd, cmdDone, 5*time.Second)
			return startRuntimeResult{err: ErrAbort}
		}

		select {
		case <-startCtx.Done():
			r.killProcess(cmd, cmdDone, 5*time.Second)
			return startRuntimeResult{err: ErrAbort}
		case <-cmdDone:
			return startRuntimeResult{err: fmt.Errorf("upstream command exited prematurely")}
		case <-time.After(time.Second):
		}
	}

	return startRuntimeResult{cmd: cmd, cmdDone: cmdDone, handlerFn: handlerFn}
}

func (r *CommandRuntime) killProcess(cmd *exec.Cmd, cmdDone <-chan struct{}, gracefulTimeout time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	if r.config.CmdStop != "" {
		stopArgs, err := config.SanitizeCommand(
			strings.ReplaceAll(r.config.CmdStop, "${PID}", fmt.Sprintf("%d", cmd.Process.Pid)),
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

func (r *CommandRuntime) Start(healthCheckTimeout time.Duration) error {
	req := startRuntimeReq{
		timeout: healthCheckTimeout,
		respond: make(chan error, 1),
	}
	r.startCh <- req
	return <-req.respond
}

func (r *CommandRuntime) Stop(gracefulTimeout time.Duration) error {
	req := stopRuntimeReq{
		timeout: gracefulTimeout,
		respond: make(chan error, 1),
	}
	r.stopCh <- req
	return <-req.respond
}

func (r *CommandRuntime) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	fn := r.handler.Load()
	if fn == nil {
		http.Error(w, "no handler available", http.StatusInternalServerError)
		return
	}
	(*fn)(w, req)
}
