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
	StateStopped ProcessState = ProcessState("stopped")
	StateReady   ProcessState = ProcessState("ready")
	StateFailed  ProcessState = ProcessState("failed")
)

type Process struct {
	sync.Mutex

	ID                 string
	config             ModelConfig
	cmd                *exec.Cmd
	logMonitor         *LogMonitor
	healthCheckTimeout int

	lastRequestHandled time.Time

	stateMutex sync.RWMutex
	state      ProcessState

	inFlightRequests sync.WaitGroup
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

// start the process and returns when it is ready
func (p *Process) start() error {

	p.stateMutex.Lock()
	defer p.stateMutex.Unlock()

	if p.state == StateReady {
		return nil
	}

	if p.state == StateFailed {
		return fmt.Errorf("process is in a failed state and can not be restarted")
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

	if err != nil {
		return err
	}

	// One of three things can happen at this stage:
	// 1. The command exits unexpectedly
	// 2. The health check fails
	// 3. The health check passes
	//
	// only in the third case will the process be considered Ready to accept
	healthCheckContext, cancelHealthCheck := context.WithCancelCause(context.Background())
	defer cancelHealthCheck(nil) // clean up
	cmdWaitChan := make(chan error, 1)
	healthCheckChan := make(chan error, 1)

	go func() {
		// possible cmd exits early
		cmdWaitChan <- p.cmd.Wait()
	}()

	go func() {
		<-time.After(250 * time.Millisecond) // give process a bit of time to start
		healthCheckChan <- p.checkHealthEndpoint(healthCheckContext)
	}()

	select {
	case err := <-cmdWaitChan:
		p.state = StateFailed
		if err != nil {
			err = fmt.Errorf("command [%s] %s", strings.Join(p.cmd.Args, " "), err.Error())
		} else {
			err = fmt.Errorf("command [%s] exited unexpected", strings.Join(p.cmd.Args, " "))
		}
		cancelHealthCheck(err)
		return err
	case err := <-healthCheckChan:
		if err != nil {
			p.state = StateFailed
			return err
		}
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

	p.state = StateReady
	return nil
}

func (p *Process) Stop() {
	// wait for any inflight requests before proceeding
	p.inFlightRequests.Wait()

	p.stateMutex.Lock()
	defer p.stateMutex.Unlock()

	if p.state != StateReady {
		fmt.Fprintf(p.logMonitor, "!!! Stop() called but Process State is not READY")
		return
	}

	if p.cmd == nil || p.cmd.Process == nil {
		// this situation should never happen... but if it does just update the state
		fmt.Fprintf(p.logMonitor, "!!! State is Ready but Command is nil.")
		p.state = StateStopped
		return
	}

	// Pretty sure this stopping code needs some work for windows and
	// will be a source of pain in the future.

	if p.config.CmdStop != "" {
		// for issue #35 to do things like `docker stop`
		args, err := p.config.SanitizeCommandStop()
		if err != nil {
			fmt.Fprintf(p.logMonitor, "!!! Error sanitizing stop command: %v", err)

			// leave the state as it is?
			return
		}

		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = p.logMonitor
		cmd.Stderr = p.logMonitor
		err = cmd.Start()
		if err != nil {
			fmt.Fprintf(p.logMonitor, "!!! Error running stop command: %v", err)

			// leave the state as it is?
			return
		}
	} else {
		sigtermTimeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		sigtermNormal := make(chan error, 1)
		go func() {
			sigtermNormal <- p.cmd.Wait()
		}()

		p.cmd.Process.Signal(syscall.SIGTERM)

		select {
		case <-sigtermTimeout.Done():
			fmt.Fprintf(p.logMonitor, "XXX Process for %s timed out waiting to stop, sending SIGKILL to PID: %d\n", p.ID, p.cmd.Process.Pid)
			p.cmd.Process.Kill()
			p.cmd.Wait()
		case err := <-sigtermNormal:
			if err != nil {
				if err.Error() != "wait: no child processes" {
					// possible that simple-responder for testing is just not
					// existing right, so suppress those errors.
					fmt.Fprintf(p.logMonitor, "!!! process for %s stopped with error > %v\n", p.ID, err)
				}
			}
		}
	}

	p.state = StateStopped
}

func (p *Process) CurrentState() ProcessState {
	p.stateMutex.RLock()
	defer p.stateMutex.RUnlock()
	return p.state
}

func (p *Process) checkHealthEndpoint(ctxFromStart context.Context) error {
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

		ctx, cancel := context.WithTimeout(ctxFromStart, time.Second)
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

		time.Sleep(time.Second)
	}
}

func (p *Process) ProxyRequest(w http.ResponseWriter, r *http.Request) {

	p.inFlightRequests.Add(1)

	defer func() {
		p.lastRequestHandled = time.Now()
		p.inFlightRequests.Done()
	}()

	if p.CurrentState() != StateReady {
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
