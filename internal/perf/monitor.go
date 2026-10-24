// Package perf collects system and GPU stats for the Performance page, the
// /metrics endpoint and the MCP sys tools.
//
// GPU stats come from probes (see package probe). Each section ticks on its
// own timer. With poll: active a section only reads while a model is loaded
// and someone is watching, so monitoring never keeps an idle GPU awake.
package perf

import (
	"context"
	"errors"
	"io"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/hw"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/perf/probe"
	"github.com/mostlygeek/llama-swap/internal/ring"
)

// History is how long readings are kept for the Performance page.
const History = time.Hour

// listenerBuffer is the per-watcher event queue. A full queue drops events
// rather than block readings.
const listenerBuffer = 64

type Monitor struct {
	log      *logmon.Monitor
	goos     string
	hardware *hw.HardwareSnapshot

	// replaced by tests
	now      func() time.Time
	readSys  func() (SysStat, error)
	newProbe func(name string, opts probe.Options) (probe.Probe, error)

	mu           sync.Mutex
	conf         config.PerformanceConfig
	sysRing      ring.Buffer[SysStat]
	gpuRing      ring.Buffer[GpuSnapshot]
	lastSysRead  time.Time
	lastGPURead  time.Time
	status       Status
	gpus         []GpuInfo
	gpuIndex     map[string]int
	modelsLoaded func() bool
	viewers      int
	listeners    map[chan Event]struct{}

	// probes run in priority order. fallback is built the first time every
	// auto-picked probe fails. failed probes are skipped until polling
	// resumes after an idle period.
	probes      []probe.Probe
	fallback    []probe.Probe
	useFallback bool
	failed      map[string]bool

	// gpuReadMu serializes GPU reads between the ticker and on-demand reads.
	gpuReadMu sync.Mutex
	sysReadMu sync.Mutex

	stopCancel context.CancelFunc
	wakeSys    chan struct{}
	wakeGPU    chan struct{}
	wg         sync.WaitGroup
}

// New builds a monitor. hardware picks probes for gpu.probes: [auto] and may
// be nil. Call Start to begin reading.
func New(c config.PerformanceConfig, logger *logmon.Monitor, hardware *hw.HardwareSnapshot) (*Monitor, error) {
	if logger == nil {
		return nil, errors.New("logger is required")
	}
	m := &Monitor{
		log:       logger,
		goos:      runtime.GOOS,
		hardware:  hardware,
		now:       time.Now,
		readSys:   ReadSysStats,
		newProbe:  probe.New,
		listeners: make(map[chan Event]struct{}),
	}
	m.applyConfig(c)
	return m, nil
}

// applyConfig resets the buffers and probes for c. The caller must hold no
// locks and the read loops must be stopped.
func (m *Monitor) applyConfig(c config.PerformanceConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	closeProbes(m.probes)
	closeProbes(m.fallback)

	m.conf = c
	m.sysRing = ring.NewBuffer[SysStat](ringCapacity(c.System.Every))
	m.gpuRing = ring.NewBuffer[GpuSnapshot](ringCapacity(c.GPU.Every))
	m.lastSysRead, m.lastGPURead = time.Time{}, time.Time{}
	m.gpus, m.gpuIndex = nil, map[string]int{}
	m.failed = map[string]bool{}
	m.probes, m.fallback, m.useFallback = nil, nil, false
	if !c.Disabled && !c.GPU.Disabled {
		m.probes = m.buildProbes(m.probeNames())
	}
	m.status = Status{System: m.sectionState(c.System.Disabled), GPU: m.sectionState(c.GPU.Disabled)}
}

func (m *Monitor) sectionState(disabled bool) SectionState {
	if m.conf.Disabled || disabled {
		return StateDisabled
	}
	return StatePaused
}

func ringCapacity(every time.Duration) int {
	if every <= 0 {
		return 1
	}
	return max(1, int(History/every))
}

// ProbeNames returns the GPU probes the config selects, with auto expanded
// from the detected hardware.
func (m *Monitor) ProbeNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.probeNames())
}

// probeNames resolves gpu.probes, expanding auto from the hardware. Callers
// hold m.mu.
func (m *Monitor) probeNames() []string {
	if m.conf.GPU.UsesAutoProbes() {
		return autoProbes(m.goos, m.hardware)
	}
	return m.conf.GPU.Probes
}

func (m *Monitor) buildProbes(names []string) []probe.Probe {
	probes := make([]probe.Probe, 0, len(names))
	for _, name := range names {
		s := m.conf.GPU.Settings[name]
		p, err := m.newProbe(name, probe.Options{Path: s.Path, Socket: s.Socket, SkipSuspended: m.conf.GPU.SkipSuspended})
		if err != nil {
			m.log.Infof("GPU probe %s: %s", name, err.Error())
			continue
		}
		probes = append(probes, p)
	}
	return probes
}

func closeProbes(probes []probe.Probe) {
	for _, p := range probes {
		if c, ok := p.(io.Closer); ok {
			c.Close()
		}
	}
}

// Enabled reports whether performance monitoring is turned on.
func (m *Monitor) Enabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.conf.Disabled
}

// SetModelsLoaded sets the check poll: active uses to see whether a model is
// loaded. The server sets it, since it owns the model processes.
func (m *Monitor) SetModelsLoaded(f func() bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modelsLoaded = f
}

// Start begins the read loops. It does nothing when monitoring is disabled
// or already running.
func (m *Monitor) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopCancel != nil || m.conf.Disabled {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.stopCancel = cancel
	m.wakeSys = make(chan struct{}, 1)
	m.wakeGPU = make(chan struct{}, 1)

	if !m.conf.System.Disabled {
		m.wg.Add(1)
		go m.loop(ctx, m.conf.System.Every, m.wakeSys, m.tickSys)
	}
	if !m.conf.GPU.Disabled {
		m.wg.Add(1)
		go m.loop(ctx, m.conf.GPU.Every, m.wakeGPU, m.tickGPU)
	}
}

// Stop ends the read loops and waits for them.
func (m *Monitor) Stop() {
	m.mu.Lock()
	cancel := m.stopCancel
	m.stopCancel = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		m.wg.Wait()
	}
}

// UpdateConfig applies a reloaded config, restarting the loops when it
// changed.
func (m *Monitor) UpdateConfig(c config.PerformanceConfig) {
	m.mu.Lock()
	same := reflect.DeepEqual(m.conf, c)
	m.mu.Unlock()
	if same {
		return
	}
	m.Stop()
	m.applyConfig(c)
	m.Start()
	m.mu.Lock()
	gpus := slices.Clone(m.gpus)
	status := m.cloneStatus()
	m.mu.Unlock()
	m.broadcast(Event{Kind: EventMeta, GPUs: gpus})
	m.broadcast(Event{Kind: EventStatus, Status: &status})
}

// loop calls tick on every interval, and right away when a watcher arrives.
func (m *Monitor) loop(ctx context.Context, every time.Duration, wake <-chan struct{}, tick func(context.Context)) {
	defer m.wg.Done()
	t := time.NewTicker(every)
	defer t.Stop()
	tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-wake:
		}
		tick(ctx)
	}
}

// shouldRead applies the poll rule. Callers hold m.mu.
func (m *Monitor) shouldRead(poll config.PerformancePoll) bool {
	if poll == config.PerformancePollAlways {
		return true
	}
	return m.viewers > 0 && m.modelLoaded()
}

func (m *Monitor) modelLoaded() bool {
	return m.modelsLoaded != nil && m.modelsLoaded()
}

func (m *Monitor) tickSys(ctx context.Context) {
	m.mu.Lock()
	read := m.shouldRead(m.conf.System.Poll)
	m.mu.Unlock()
	if !read {
		m.setState(func(s *Status) { s.System = StatePaused })
		return
	}
	m.readSystem()
}

func (m *Monitor) tickGPU(ctx context.Context) {
	m.mu.Lock()
	read := m.shouldRead(m.conf.GPU.Poll)
	m.mu.Unlock()
	if !read {
		m.setState(func(s *Status) { s.GPU = StatePaused })
		return
	}
	m.readGPU(ctx)
}

func (m *Monitor) readSystem() {
	m.sysReadMu.Lock()
	defer m.sysReadMu.Unlock()

	s, err := m.readSys()
	if err != nil {
		m.log.Errorf("failed to read system stats: %s", err.Error())
		return
	}
	m.mu.Lock()
	m.sysRing.Push(s)
	m.lastSysRead = s.Timestamp
	m.mu.Unlock()
	m.setState(func(st *Status) { st.System = StateOK })
	m.broadcast(Event{Kind: EventSys, Sys: &s})
}

// readGPU runs the probes once and stores the merged result.
func (m *Monitor) readGPU(ctx context.Context) {
	m.gpuReadMu.Lock()
	defer m.gpuReadMu.Unlock()

	now := m.now()
	m.mu.Lock()
	if now.Sub(m.lastGPURead) > 2*m.conf.GPU.Every {
		// polling resumed after an idle period: retry failed probes
		clear(m.failed)
		m.useFallback = false
	}
	probes := m.runnable()
	m.mu.Unlock()

	samples, ok := m.runProbes(ctx, probes)
	if !ok {
		m.mu.Lock()
		if m.conf.GPU.UsesAutoProbes() && !m.useFallback {
			// every picked probe failed, e.g. a container without rocm-smi
			m.useFallback = true
			if m.fallback == nil {
				m.fallback = m.buildProbes(allProbes(m.goos))
			}
			probes = m.runnable()
			m.mu.Unlock()
			samples, _ = m.runProbes(ctx, probes)
		} else {
			m.mu.Unlock()
		}
	}

	m.mu.Lock()
	samples = m.nameAndFilter(samples)
	snap := GpuSnapshot{Timestamp: now, GPUs: samples}
	m.gpuRing.Push(snap)
	m.lastGPURead = now
	metaChanged := m.indexGPUs(samples)
	gpus := slices.Clone(m.gpus)
	m.mu.Unlock()

	if metaChanged {
		m.broadcast(Event{Kind: EventMeta, GPUs: gpus})
	}
	var suspended []string
	for _, s := range samples {
		if s.State == probe.GpuStateSuspended {
			suspended = append(suspended, s.Key)
		}
	}
	m.setState(func(st *Status) {
		st.GPU = StateOK
		st.Suspended = suspended
	})
	m.broadcast(Event{Kind: EventGPU, GPU: &snap})
}

// runnable returns the probes to run this tick. Callers hold m.mu.
func (m *Monitor) runnable() []probe.Probe {
	list := m.probes
	if m.useFallback {
		list = m.fallback
	}
	var out []probe.Probe
	for _, p := range list {
		if !m.failed[p.Name()] {
			out = append(out, p)
		}
	}
	return out
}

// runProbes reads every probe in priority order and keeps the first sample
// for each GPU key. ok is false when no probe succeeded.
func (m *Monitor) runProbes(ctx context.Context, probes []probe.Probe) (samples []probe.GpuSample, ok bool) {
	seen := map[string]bool{}
	for _, p := range probes {
		got, err := p.Read(ctx)
		if err != nil {
			m.mu.Lock()
			m.failed[p.Name()] = true
			m.mu.Unlock()
			if errors.Is(err, probe.ErrUnavailable) {
				m.log.Debugf("GPU probe %s unavailable: %s", p.Name(), err.Error())
			} else {
				m.log.Warnf("GPU probe %s failed: %s", p.Name(), err.Error())
			}
			continue
		}
		ok = true
		for _, s := range got {
			if !seen[s.Key] {
				seen[s.Key] = true
				samples = append(samples, s)
			}
		}
	}
	return samples, ok
}

// nameAndFilter gives suspended GPUs their last known name and drops
// excluded GPUs. Callers hold m.mu.
func (m *Monitor) nameAndFilter(samples []probe.GpuSample) []probe.GpuSample {
	out := samples[:0]
	for _, s := range samples {
		if i, ok := m.gpuIndex[s.Key]; ok && s.State == probe.GpuStateSuspended {
			s.Name = m.gpus[i].Name
		}
		if m.excluded(s) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func (m *Monitor) excluded(s probe.GpuSample) bool {
	for _, e := range m.conf.GPU.Exclude {
		if e == s.Key || (e != "" && strings.Contains(s.Name, e)) {
			return true
		}
	}
	return false
}

// indexGPUs adds new GPUs to the index order and updates names. It reports
// whether the list changed. Callers hold m.mu.
func (m *Monitor) indexGPUs(samples []probe.GpuSample) bool {
	changed := false
	for _, s := range samples {
		i, ok := m.gpuIndex[s.Key]
		if !ok {
			m.gpuIndex[s.Key] = len(m.gpus)
			m.gpus = append(m.gpus, GpuInfo{Key: s.Key, Name: s.Name})
			changed = true
			continue
		}
		if s.State == probe.GpuStateOK && s.Name != "" && m.gpus[i].Name != s.Name {
			m.gpus[i].Name = s.Name
			changed = true
		}
	}
	return changed
}

// setState applies f and tells watchers when the status changed.
func (m *Monitor) setState(f func(*Status)) {
	m.mu.Lock()
	before := m.cloneStatus()
	f(&m.status)
	after := m.cloneStatus()
	m.mu.Unlock()
	if !reflect.DeepEqual(before, after) {
		m.broadcast(Event{Kind: EventStatus, Status: &after})
	}
}

func (m *Monitor) cloneStatus() Status {
	s := m.status
	s.Suspended = slices.Clone(s.Suspended)
	return s
}

func (m *Monitor) broadcast(e Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for l := range m.listeners {
		select {
		case l <- e:
		default:
		}
	}
}

// WatchState is what a new watcher needs before live events: the GPU index
// order, the last hour of readings and the current status.
type WatchState struct {
	SysEvery time.Duration // 0 when system stats are disabled
	GPUEvery time.Duration // 0 when GPU stats are disabled
	GPUs     []GpuInfo
	Sys      []SysStat
	GPU      []GpuSnapshot
	Status   Status
}

// Watch registers a viewer, which lets poll: active sections read, and
// returns the history plus a channel of live events. stop must be called
// when the viewer leaves.
func (m *Monitor) Watch() (WatchState, <-chan Event, func()) {
	ch := make(chan Event, listenerBuffer)
	m.mu.Lock()
	m.listeners[ch] = struct{}{}
	m.viewers++
	first := m.viewers == 1
	state := m.watchState()
	wakes := []chan struct{}{m.wakeSys, m.wakeGPU}
	m.mu.Unlock()

	if first {
		// read now rather than up to one interval later
		for _, wake := range wakes {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}

	var once sync.Once
	stop := func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.listeners, ch)
			m.viewers--
			m.mu.Unlock()
		})
	}
	return state, ch, stop
}

// watchState snapshots the history. Callers hold m.mu.
func (m *Monitor) watchState() WatchState {
	cutoff := m.now().Add(-History)
	ws := WatchState{GPUs: slices.Clone(m.gpus), Status: m.cloneStatus()}
	if !m.conf.Disabled && !m.conf.System.Disabled {
		ws.SysEvery = m.conf.System.Every
	}
	if !m.conf.Disabled && !m.conf.GPU.Disabled {
		ws.GPUEvery = m.conf.GPU.Every
	}
	for _, s := range m.sysRing.Slice() {
		if s.Timestamp.After(cutoff) {
			ws.Sys = append(ws.Sys, s)
		}
	}
	for _, g := range m.gpuRing.Slice() {
		if g.Timestamp.After(cutoff) {
			ws.GPU = append(ws.GPU, g)
		}
	}
	return ws
}

// Latest returns the newest readings for /metrics and the MCP tools. Under
// poll: active it first takes a fresh reading when a model is loaded and the
// last one is older than every. A reading older than two intervals is stale
// and returned as nil, so a paused GPU is reported as not sampled instead of
// with old values.
func (m *Monitor) Latest(ctx context.Context) (*SysStat, *GpuSnapshot) {
	m.mu.Lock()
	conf := m.conf
	running := m.stopCancel != nil
	loaded := m.modelLoaded()
	now := m.now()
	sysDue := running && !conf.System.Disabled && conf.System.Poll == config.PerformancePollActive &&
		loaded && now.Sub(m.lastSysRead) >= conf.System.Every
	gpuDue := running && !conf.GPU.Disabled && conf.GPU.Poll == config.PerformancePollActive &&
		loaded && now.Sub(m.lastGPURead) >= conf.GPU.Every
	m.mu.Unlock()

	if sysDue {
		m.readSystem()
	}
	if gpuDue {
		m.readGPU(ctx)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	now = m.now()
	var sys *SysStat
	if s := m.sysRing.Slice(); len(s) > 0 && now.Sub(s[len(s)-1].Timestamp) <= 2*conf.System.Every {
		sys = &s[len(s)-1]
	}
	var gpu *GpuSnapshot
	if g := m.gpuRing.Slice(); len(g) > 0 && now.Sub(g[len(g)-1].Timestamp) <= 2*conf.GPU.Every {
		gpu = &g[len(g)-1]
	}
	return sys, gpu
}

// GPUName returns the last known name of a GPU key.
func (m *Monitor) GPUName(key string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i, ok := m.gpuIndex[key]; ok {
		return m.gpus[i].Name
	}
	return ""
}
