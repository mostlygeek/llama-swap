package proxy

import (
	"fmt"
	"net/http"
	"slices"
	"sync"

	"github.com/mostlygeek/llama-swap/proxy/config"
)

type ProcessGroup struct {
	sync.Mutex

	config     config.Config
	id         string
	swap       bool
	exclusive  bool
	persistent bool

	proxyLogger    *LogMonitor
	upstreamLogger *LogMonitor

	// map of current processes
	processes       map[string]*Process
	lastUsedProcess string

	// inflight tracks fast-path requests (requests for the already-selected
	// model in a swap group). Fast-path requests Add(1) while holding pg.Lock
	// and Done() on completion; a concurrent swap request calls inflight.Wait()
	// under pg.Lock before stopping the current process. Without this tracking,
	// a fast-path request that has released pg.Lock but has not yet called
	// Process.inFlightRequests.Add(1) races with Stop()'s Wait() and can be
	// killed mid-request.
	inflight sync.WaitGroup

	// testDelayFastPath is a test-only hook that, when non-nil, blocks in the
	// fast path after pg.Lock is released but before the request is dispatched
	// to Process.ProxyRequest. It exists solely to make the fast-path vs swap
	// race deterministically reproducible in tests.
	testDelayFastPath chan struct{}
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
	}

	// Create a Process for each member in the group
	for _, modelID := range groupConfig.Members {
		modelConfig, modelID, _ := pg.config.FindConfig(modelID)
		processLogger := NewLogMonitorWriter(upstreamLogger)
		process := NewProcess(modelID, pg.config.HealthCheckTimeout, modelConfig, processLogger, pg.proxyLogger)
		pg.processes[modelID] = process
	}

	return pg
}

// ProxyRequest proxies a request to the specified model
func (pg *ProcessGroup) ProxyRequest(modelID string, writer http.ResponseWriter, request *http.Request) error {
	if !pg.HasMember(modelID) {
		return fmt.Errorf("model %s not part of group %s", modelID, pg.id)
	}

	if pg.swap {
		pg.Lock()
		if pg.lastUsedProcess != modelID {

			// Wait for in-flight fast-path requests to drain before stopping
			// the previous process. Without this, a fast-path request that has
			// released pg.Lock but has not yet incremented
			// Process.inFlightRequests races with Stop() and can be killed
			// mid-request.
			pg.inflight.Wait()

			// is there something already running?
			if pg.lastUsedProcess != "" {
				pg.processes[pg.lastUsedProcess].Stop()
			}

			// wait for the request to the new model to be fully handled
			// and prevent race conditions see issue #277
			pg.processes[modelID].ProxyRequest(writer, request)
			pg.lastUsedProcess = modelID

			// short circuit and exit
			pg.Unlock()
			return nil
		}

		// Fast path: register this request in inflight before releasing
		// pg.Lock so a concurrent swap will wait for it to complete.
		pg.inflight.Add(1)
		defer pg.inflight.Done()
		pg.Unlock()

		if pg.testDelayFastPath != nil {
			<-pg.testDelayFastPath
		}
	}

	pg.processes[modelID].ProxyRequest(writer, request)
	return nil
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
	pg.Lock()

	process, exists := pg.processes[modelID]
	if !exists {
		pg.Unlock()
		return fmt.Errorf("process not found for %s", modelID)
	}

	if pg.lastUsedProcess == modelID {
		pg.lastUsedProcess = ""
	}
	pg.Unlock()

	switch strategy {
	case StopImmediately:
		process.StopImmediately()
	default:
		process.Stop()
	}
	return nil
}

func (pg *ProcessGroup) StopProcesses(strategy StopStrategy) {
	pg.Lock()
	defer pg.Unlock()

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
}
