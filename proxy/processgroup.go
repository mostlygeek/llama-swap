package proxy

import (
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/proxy/config"
)

type ProcessGroup struct {
	mu   sync.Mutex
	cond *sync.Cond

	config     config.Config
	id         string
	swap       bool
	exclusive  bool
	persistent bool

	proxyLogger    *LogMonitor
	upstreamLogger *LogMonitor

	// map of current processes
	processes map[string]*Process

	// swap coordination state (only used when swap == true)
	activeModel   string         // currently loaded model
	inFlight      int            // in-flight request count for activeModel
	pendingCount  map[string]int // goroutines waiting to use each model
	unloadDelayCh chan struct{}  // non-nil while an unload delay is active; close to cancel
}

func NewProcessGroup(id string, config config.Config, proxyLogger *LogMonitor, upstreamLogger *LogMonitor) *ProcessGroup {
	groupConfig, ok := config.Groups[id]
	if !ok {
		panic("Unable to find configuration for group id: " + id)
	}

	pg := &ProcessGroup{
		id:             id,
		config:         config,
		swap:           groupConfig.Swap,
		exclusive:      groupConfig.Exclusive,
		persistent:     groupConfig.Persistent,
		proxyLogger:    proxyLogger,
		upstreamLogger: upstreamLogger,
		processes:      make(map[string]*Process),
		pendingCount:   make(map[string]int),
	}
	pg.cond = sync.NewCond(&pg.mu)

	// Create a Process for each member in the group
	for _, modelID := range groupConfig.Members {
		modelConfig, modelID, _ := pg.config.FindConfig(modelID)
		processLogger := NewLogMonitorWriter(upstreamLogger)
		process := NewProcess(modelID, pg.config.HealthCheckTimeout, modelConfig, processLogger, pg.proxyLogger)
		pg.processes[modelID] = process
	}

	return pg
}

// ProxyRequest proxies a request to the specified model.
//
// When swap is true, requests are serialized through a priority-aware
// coordination layer:
//   - Models with a higher Priority value preempt lower-priority models
//     once the currently in-flight request for the lower-priority model
//     completes (the in-flight request is never aborted).
//   - Models with equal priority compete fairly; the currently loaded
//     model keeps running until all its pending requests drain before a
//     different model of the same priority can swap in.
//   - A model with UnloadDelay > 0 stays loaded for that many seconds
//     after its last request, blocking lower-or-equal-priority swaps.
//     A strictly higher-priority model bypasses the delay immediately.
func (pg *ProcessGroup) ProxyRequest(modelID string, writer http.ResponseWriter, request *http.Request) error {
	if !pg.HasMember(modelID) {
		return fmt.Errorf("model %s not part of group %s", modelID, pg.id)
	}

	if !pg.swap {
		pg.processes[modelID].ProxyRequest(writer, request)
		return nil
	}

	pg.mu.Lock()
	pg.pendingCount[modelID]++

	loggedWait := false
	for {
		canProceed := false

		if pg.activeModel == modelID {
			// Already the active model. Proceed unless a higher-priority model
			// is waiting — in that case yield so it can swap in after the
			// current in-flight request finishes.
			if !pg.hasHigherPriorityPendingLocked(modelID) {
				// A new request for the active model cancels any running delay
				// (the delay timer resets when this request finishes).
				pg.cancelUnloadDelayLocked()
				canProceed = true
			}
		} else if pg.inFlight == 0 && pg.isHighestPriorityLocked(modelID) {
			// Candidate for a swap. Check whether the active model is within
			// its unload delay.
			if pg.unloadDelayCh != nil {
				myPriority := pg.getModelPriorityLocked(modelID)
				activePriority := pg.getModelPriorityLocked(pg.activeModel)
				if myPriority > activePriority {
					// Strictly higher priority: bypass the delay.
					pg.proxyLogger.Infof(
						"<%s> bypassing %ds unload delay for %q: higher priority (%d > %d)",
						modelID, pg.getModelUnloadDelayLocked(pg.activeModel),
						pg.activeModel, myPriority, activePriority,
					)
					pg.cancelUnloadDelayLocked()
					canProceed = true
				}
				// equal/lower priority: fall through to Wait
			} else {
				canProceed = true
			}

			if canProceed {
				if pg.activeModel != "" {
					// Stop the previous model while holding the lock.
					// inFlight == 0 means the process's own WaitGroup is also
					// zero, so Stop's internal Wait returns immediately.
					pg.processes[pg.activeModel].Stop()
				}
				pg.activeModel = modelID
			}
		}

		if canProceed {
			break
		}

		if !loggedWait {
			if blocker := pg.highestPriorityPendingLocked(modelID); blocker != "" {
				pg.proxyLogger.Infof("<%s> waiting: higher priority model %q (priority %d) is pending",
					modelID, blocker, pg.getModelPriorityLocked(blocker))
			} else if pg.unloadDelayCh != nil && pg.activeModel != modelID {
				pg.proxyLogger.Infof("<%s> waiting: %q has an active %ds unload delay",
					modelID, pg.activeModel, pg.getModelUnloadDelayLocked(pg.activeModel))
			}
			loggedWait = true
		}
		pg.cond.Wait()
	}

	pg.inFlight++
	pg.pendingCount[modelID]--
	pg.mu.Unlock()

	// Serve the request. process.ProxyRequest starts the process lazily if
	// it is not yet running. The lock is not held during serving so that
	// concurrent requests to the same model can proceed in parallel.
	pg.processes[modelID].ProxyRequest(writer, request)

	pg.mu.Lock()
	pg.inFlight--
	if pg.inFlight == 0 {
		delay := pg.getModelUnloadDelayLocked(pg.activeModel)
		if delay > 0 {
			pg.proxyLogger.Debugf("<%s> starting %ds unload delay", pg.activeModel, delay)
			ch := make(chan struct{})
			pg.unloadDelayCh = ch
			captured := pg.activeModel
			go func() {
				select {
				case <-time.After(time.Duration(delay) * time.Second):
					pg.mu.Lock()
					if pg.unloadDelayCh == ch {
						pg.unloadDelayCh = nil
						pg.proxyLogger.Debugf("<%s> unload delay expired", captured)
						pg.cond.Broadcast()
					}
					pg.mu.Unlock()
				case <-ch:
					// cancelled: new same-model request or higher-priority bypass
				}
			}()
		}
		pg.cond.Broadcast()
	}
	pg.mu.Unlock()

	return nil
}

// cancelUnloadDelayLocked cancels any active unload delay.
// Must be called with pg.mu held.
func (pg *ProcessGroup) cancelUnloadDelayLocked() {
	if pg.unloadDelayCh != nil {
		close(pg.unloadDelayCh)
		pg.unloadDelayCh = nil
	}
}

// isHighestPriorityLocked returns true when no other pending model has a
// strictly higher priority than modelID. Must be called with pg.mu held.
func (pg *ProcessGroup) isHighestPriorityLocked(modelID string) bool {
	return pg.highestPriorityPendingLocked(modelID) == ""
}

// hasHigherPriorityPendingLocked returns true when at least one other pending
// model has a strictly higher priority than modelID. Must be called with
// pg.mu held.
func (pg *ProcessGroup) hasHigherPriorityPendingLocked(modelID string) bool {
	return pg.highestPriorityPendingLocked(modelID) != ""
}

// highestPriorityPendingLocked returns the model ID of the highest-priority
// pending model that has strictly higher priority than modelID, or "" if none.
// Must be called with pg.mu held.
func (pg *ProcessGroup) highestPriorityPendingLocked(modelID string) string {
	myPriority := pg.getModelPriorityLocked(modelID)
	bestID := ""
	bestPriority := myPriority
	for otherID, count := range pg.pendingCount {
		if otherID == modelID || count == 0 {
			continue
		}
		if p := pg.getModelPriorityLocked(otherID); p > bestPriority {
			bestPriority = p
			bestID = otherID
		}
	}
	return bestID
}

// getModelPriorityLocked returns the configured priority for a model.
// Must be called with pg.mu held.
func (pg *ProcessGroup) getModelPriorityLocked(modelID string) int {
	if mc, _, ok := pg.config.FindConfig(modelID); ok {
		return mc.Priority
	}
	return 0
}

// getModelUnloadDelayLocked returns the configured unload delay in seconds.
// Must be called with pg.mu held.
func (pg *ProcessGroup) getModelUnloadDelayLocked(modelID string) int {
	if mc, _, ok := pg.config.FindConfig(modelID); ok {
		return mc.UnloadDelay
	}
	return 0
}

func (pg *ProcessGroup) HasMember(modelName string) bool {
	return slices.Contains(pg.config.Groups[pg.id].Members, modelName)
}

func (pg *ProcessGroup) GetMember(modelName string) (*Process, bool) {
	if pg.HasMember(modelName) {
		return pg.processes[modelName], true
	}
	return nil, false
}

func (pg *ProcessGroup) StopProcess(modelID string, strategy StopStrategy) error {
	pg.mu.Lock()

	process, exists := pg.processes[modelID]
	if !exists {
		pg.mu.Unlock()
		return fmt.Errorf("process not found for %s", modelID)
	}

	if pg.activeModel == modelID {
		pg.cancelUnloadDelayLocked()
		pg.activeModel = ""
	}
	pg.mu.Unlock()

	switch strategy {
	case StopImmediately:
		process.StopImmediately()
	default:
		process.Stop()
	}

	pg.mu.Lock()
	pg.cond.Broadcast()
	pg.mu.Unlock()

	return nil
}

func (pg *ProcessGroup) StopProcesses(strategy StopStrategy) {
	pg.mu.Lock()
	defer func() {
		pg.cancelUnloadDelayLocked()
		pg.activeModel = ""
		pg.cond.Broadcast()
		pg.mu.Unlock()
	}()

	if len(pg.processes) == 0 {
		return
	}

	// stop Processes in parallel
	var wg sync.WaitGroup
	for _, process := range pg.processes {
		wg.Add(1)
		go func(process *Process) {
			defer wg.Done()
			switch strategy {
			case StopImmediately:
				process.StopImmediately()
			default:
				process.Stop()
			}
		}(process)
	}
	wg.Wait()
}

func (pg *ProcessGroup) Shutdown() {
	var wg sync.WaitGroup
	for _, process := range pg.processes {
		wg.Add(1)
		go func(process *Process) {
			defer wg.Done()
			process.Shutdown()
		}(process)
	}
	wg.Wait()

	pg.mu.Lock()
	pg.cancelUnloadDelayLocked()
	pg.activeModel = ""
	pg.cond.Broadcast()
	pg.mu.Unlock()
}
