package process

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"
)

const (
	// busyPollInterval is how often a busy check endpoint is polled while the
	// process is ready.
	busyPollInterval = time.Second

	// busyProbeTimeout bounds a single probe of the busy check endpoint.
	busyProbeTimeout = 2 * time.Second

	// busyMaxProbeFailures is how many consecutive failed probes keep the last
	// known busy state. After that the upstream is assumed idle, so an
	// endpoint that stopped answering cannot pin a model in memory forever.
	busyMaxProbeFailures = 5
)

// Busy reports whether the upstream said, on its last busy check, that it is
// still working on something no open HTTP request represents, or said so less
// than busyCheck.hold seconds ago. It never blocks
// and is always false when no busyCheck is configured.
func (p *ProcessCommand) Busy() bool {
	return p.busy.Load() || time.Now().UnixNano() < p.busyHoldUntil.Load()
}

// kickBusyCheck asks the poller to probe right away. It is called after every
// proxied request, because that is when asynchronous work usually appears: a
// job submission returns immediately and leaves no request open behind it.
func (p *ProcessCommand) kickBusyCheck() {
	if !p.config.BusyCheck.Enabled() {
		return
	}
	select {
	case p.busyKick <- struct{}{}:
	default:
	}
}

// pollBusy probes the busy check endpoint until the process leaves
// StateReady. It is started when the process becomes ready.
func (p *ProcessCommand) pollBusy(handler http.HandlerFunc) {
	defer func() {
		p.busy.Store(false)
		p.busyHoldUntil.Store(0)
	}()

	ticker := time.NewTicker(busyPollInterval)
	defer ticker.Stop()

	failures := 0
	for {
		if p.State() != StateReady {
			return
		}

		busy, err := p.probeBusy(handler)
		switch {
		case err == nil:
			failures = 0
			p.setBusy(busy)
		case failures < busyMaxProbeFailures:
			failures++
		default:
			p.setBusy(false)
		}

		select {
		case <-ticker.C:
		case <-p.busyKick:
		}
	}
}

func (p *ProcessCommand) setBusy(busy bool) {
	was := p.busy.Swap(busy)
	if was == busy {
		return
	}
	p.proxyLogger.Debugf("<%s> busy check: busy=%v", p.id, busy)
	if was && !busy {
		if hold := time.Duration(p.config.BusyCheck.Hold) * time.Second; hold > 0 {
			p.busyHoldUntil.Store(time.Now().Add(hold).UnixNano())
			p.proxyLogger.Debugf("<%s> busy check: holding for %v so clients can fetch results", p.id, hold)
		}
	} else if busy {
		p.busyHoldUntil.Store(0)
	}
}

// probeBusy asks the upstream, through its reverse proxy handler, whether it
// is busy. The handler is called directly rather than through ServeHTTP so
// the probe neither counts as in-flight nor refreshes the TTL clock.
func (p *ProcessCommand) probeBusy(handler http.HandlerFunc) (bool, error) {
	ctx, cancel := context.WithTimeout(p.parentCtx, busyProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.config.BusyCheck.Endpoint, nil)
	if err != nil {
		return false, err
	}
	rr := httptest.NewRecorder()
	handler(rr, req)
	resp := rr.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, errBusyProbeStatus
	}
	return parseBusy(rr.Body.Bytes(), p.config.BusyCheck.Fields)
}

type busyProbeError string

func (e busyProbeError) Error() string { return string(e) }

const errBusyProbeStatus = busyProbeError("busy check endpoint did not return 200")

// parseBusy reports whether any of fields holds a value that means "working":
// a non-empty array or object, a non-zero number, a non-empty string or true.
func parseBusy(body []byte, fields []string) (bool, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return false, err
	}
	for _, f := range fields {
		switch v := doc[f].(type) {
		case []any:
			if len(v) > 0 {
				return true, nil
			}
		case map[string]any:
			if len(v) > 0 {
				return true, nil
			}
		case float64:
			if v != 0 {
				return true, nil
			}
		case string:
			if v != "" {
				return true, nil
			}
		case bool:
			if v {
				return true, nil
			}
		}
	}
	return false, nil
}
