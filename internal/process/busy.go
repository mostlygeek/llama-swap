package process

import (
	"context"
	"encoding/json"
	"math"
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
// than busyCheck.hold seconds ago. It also stays true while the probe that
// follows a finished request is still running, because that request may have
// queued new work the cached result does not show yet. It never blocks and is
// always false when no busyCheck is configured.
//
// Busy-now and the hold period share one atomic deadline (busyUntil), so a
// reader can never see an idle gap while the poller moves between them. The
// pending probe is read first: the poller updates busyUntil before it clears
// the pending marker, so a reader that sees no pending probe also sees the
// result of that probe.
func (p *ProcessCommand) Busy() bool {
	if p.kickSeq.Load() != p.probedSeq.Load() {
		return true
	}
	return time.Now().UnixNano() < p.busyUntil.Load()
}

// kickBusyCheck asks the poller to probe right away. It is called after every
// proxied request, because that is when asynchronous work usually appears: a
// job submission returns immediately and leaves no request open behind it.
// Until that probe finishes Busy() stays true, so a swap cannot slip in
// between the request finishing and the new job becoming visible.
func (p *ProcessCommand) kickBusyCheck() {
	if !p.config.BusyCheck.Enabled() {
		return
	}
	p.busyMu.Lock()
	if !p.busyPolling {
		p.busyMu.Unlock()
		return
	}
	p.kickSeq.Add(1)
	p.busyMu.Unlock()

	select {
	case p.busyKick <- struct{}{}:
	default:
	}
}

// startBusyPolling begins a new polling generation and runs its poller in the
// background. Every start gets its own generation, so a poller left over from
// a previous start can no longer change the busy state.
func (p *ProcessCommand) startBusyPolling(handler http.HandlerFunc) {
	p.busyMu.Lock()
	gen := p.busyGen.Add(1)
	p.busyPolling = true
	p.busyActive = false
	p.busyUntil.Store(0)
	p.probedSeq.Store(p.kickSeq.Load())
	p.busyMu.Unlock()

	go p.pollBusy(gen, handler)
}

// endBusyPolling clears the busy state when the poller of generation gen
// stops. It does nothing if a newer generation has taken over.
func (p *ProcessCommand) endBusyPolling(gen uint64) {
	p.busyMu.Lock()
	defer p.busyMu.Unlock()
	if p.busyGen.Load() != gen {
		return
	}
	p.busyPolling = false
	p.busyActive = false
	p.busyUntil.Store(0)
	p.probedSeq.Store(p.kickSeq.Load())
}

// pollBusy probes the busy check endpoint until the process leaves
// StateReady or a newer polling generation replaces this one.
func (p *ProcessCommand) pollBusy(gen uint64, handler http.HandlerFunc) {
	defer p.endBusyPolling(gen)

	ticker := time.NewTicker(busyPollInterval)
	defer ticker.Stop()

	failures := 0
	for {
		if p.State() != StateReady || p.busyGen.Load() != gen {
			return
		}

		// Kicks that arrive while the probe runs are newer than seq, so they
		// keep Busy() true until the next probe completes.
		seq := p.kickSeq.Load()
		busy, err := p.probeBusy(handler)

		apply := true
		switch {
		case err == nil:
			failures = 0
		case failures < busyMaxProbeFailures:
			failures++ // keep the last known state
			apply = false
		default:
			busy = false // endpoint dead: release the model
		}
		p.publishBusy(gen, seq, apply, busy)

		select {
		case <-ticker.C:
		case <-p.busyKick:
		}
	}
}

// publishBusy records the result of a probe started when kickSeq was seq. The
// busy deadline is updated before the pending marker is cleared, so there is
// no moment where neither shows the model as busy. A probe from an older
// generation is dropped.
func (p *ProcessCommand) publishBusy(gen, seq uint64, apply, busy bool) {
	p.busyMu.Lock()
	defer p.busyMu.Unlock()
	if p.busyGen.Load() != gen {
		return
	}
	if apply {
		p.setBusyLocked(busy)
	}
	p.probedSeq.Store(seq)
}

// setBusyLocked records what the busy check reported. busyMu must be held.
// Busy means "until further notice" (busyUntil = MaxInt64). Going idle moves
// the deadline to the end of the hold period, or to now without one.
func (p *ProcessCommand) setBusyLocked(busy bool) {
	if p.busyActive == busy {
		return
	}
	p.busyActive = busy
	if busy {
		p.busyUntil.Store(math.MaxInt64)
		p.proxyLogger.Debugf("<%s> busy check: busy=true", p.id)
		return
	}

	hold := time.Duration(p.config.BusyCheck.Hold) * time.Second
	p.busyUntil.Store(time.Now().Add(hold).UnixNano())
	p.proxyLogger.Debugf("<%s> busy check: busy=false", p.id)
	if hold > 0 {
		p.proxyLogger.Debugf("<%s> busy check: holding for %v so clients can fetch results", p.id, hold)
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
