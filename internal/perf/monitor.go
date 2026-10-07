package perf

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/ring"
)

var (
	ErrNotImplemented = errors.New("not implemented")
	ErrNoGpuTool      = errors.New("no GPU monitoring tool available")
)

// A GPU collector that stops is restarted after gpuRestartDelay. The delay
// doubles on each consecutive failure up to gpuRestartMaxDelay.
const (
	gpuRestartDelay    = 5 * time.Second
	gpuRestartMaxDelay = 30 * time.Second
)

type gpuStatsFunc func(ctx context.Context, every time.Duration, logger *logmon.Monitor) (chan []GpuStat, error)

// gpuWaitFunc waits d before a collector restart. It returns false if ctx is
// done first.
type gpuWaitFunc func(ctx context.Context, d time.Duration) bool

type Monitor struct {
	mutex   sync.RWMutex
	log     *logmon.Monitor
	conf    config.PerformanceConfig
	sysRing ring.Buffer[SysStat]
	gpuRing ring.Buffer[[]GpuStat]

	stopCtx    context.Context
	stopCancel context.CancelFunc

	sysListeners map[chan SysStat]struct{}
	gpuListeners map[chan []GpuStat]struct{}

	// gpuStats starts the GPU collector and gpuWait waits before a restart.
	// Tests replace both.
	gpuStats gpuStatsFunc
	gpuWait  gpuWaitFunc
}

func ringCapacity(c config.PerformanceConfig) int {
	n := int(time.Hour / c.Every)
	if n < 1 {
		n = 1
	}
	return n
}

func New(c config.PerformanceConfig, logger *logmon.Monitor) (*Monitor, error) {

	if c.Every < 100*time.Millisecond {
		c.Every = 100 * time.Millisecond
	}

	if logger == nil {
		return nil, errors.New("logger is required")
	}

	capacity := ringCapacity(c)
	return &Monitor{
		conf:         c,
		log:          logger,
		sysRing:      ring.NewBuffer[SysStat](capacity),
		gpuRing:      ring.NewBuffer[[]GpuStat](capacity),
		sysListeners: make(map[chan SysStat]struct{}),
		gpuListeners: make(map[chan []GpuStat]struct{}),

		gpuStats: getGpuStats,
		gpuWait:  waitOrDone,
	}, nil
}

func (m *Monitor) Stop() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.stopCancel == nil {
		return
	}
	m.stopCancel()
	m.stopCancel = nil
}

// UpdateConfig updates the monitor configuration and restarts if changed.
func (m *Monitor) UpdateConfig(newConf config.PerformanceConfig) {
	m.mutex.RLock()
	changed := m.conf != newConf
	m.mutex.RUnlock()

	if !changed {
		return
	}

	m.Stop()
	m.mutex.Lock()
	m.conf = newConf
	capacity := ringCapacity(newConf)
	m.sysRing = ring.NewBuffer[SysStat](capacity)
	m.gpuRing = ring.NewBuffer[[]GpuStat](capacity)
	m.mutex.Unlock()
	if !newConf.Disabled {
		m.Start()
	}
}

// Subscribe returns channels to listen to system and GPU stats.
func (m *Monitor) Subscribe() (chan SysStat, chan []GpuStat, func()) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	sysChan := make(chan SysStat, 1)
	gpuChan := make(chan []GpuStat, 1)

	m.sysListeners[sysChan] = struct{}{}
	m.gpuListeners[gpuChan] = struct{}{}

	unsub := func() {
		m.mutex.Lock()
		defer m.mutex.Unlock()
		delete(m.sysListeners, sysChan)
		delete(m.gpuListeners, gpuChan)
	}

	return sysChan, gpuChan, unsub
}

func (m *Monitor) Start() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.stopCancel != nil {
		return
	}

	m.stopCtx, m.stopCancel = context.WithCancel(context.Background())

	go func() {
		tick := time.NewTicker(m.conf.Every)
		defer tick.Stop()
		for {
			select {
			case <-m.stopCtx.Done():
				return
			case <-tick.C:
				s, err := ReadSysStats()
				if err != nil {
					if err != ErrNotImplemented {
						m.log.Errorf("failed to read sys stats: %s", err.Error())
					}
					continue
				}
				m.mutex.Lock()
				m.sysRing.Push(s)
				for l := range m.sysListeners {
					select {
					case l <- s:
					default:
					}
				}
				m.mutex.Unlock()
			}
		}
	}()

	go m.collectGpuStats(m.stopCtx, m.conf.Every)
}

// collectGpuStats reads GPU stats until ctx is done. A collector that stops
// while the monitor is running, for example because its nvidia-smi child
// exited, is restarted after a backoff so GPU stats do not stay frozen.
func (m *Monitor) collectGpuStats(ctx context.Context, every time.Duration) {
	gpuCh, err := m.gpuStats(ctx, every, m.log)
	if err != nil {
		if errors.Is(err, ErrNotImplemented) || errors.Is(err, ErrNoGpuTool) {
			m.log.Infof("GPU monitoring not available: %s", err.Error())
		} else {
			m.log.Errorf("failed to initialize GPU monitoring: %s", err.Error())
		}
		return
	}

	delay := gpuRestartDelay
	for {
		if gpuCh != nil && m.forwardGpuStats(ctx, gpuCh) {
			// it worked before stopping, so start the backoff over
			delay = gpuRestartDelay
		}
		if ctx.Err() != nil {
			return
		}

		m.log.Errorf("GPU monitoring stopped - restarting in %s", delay)
		if !m.gpuWait(ctx, delay) {
			return
		}

		gpuCh, err = m.gpuStats(ctx, every, m.log)
		if err != nil {
			m.log.Errorf("failed to restart GPU monitoring: %s", err.Error())
			gpuCh = nil
		}
		delay = min(delay*2, gpuRestartMaxDelay)
	}
}

// waitOrDone waits d and returns true, or returns false as soon as ctx is
// done.
func waitOrDone(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// forwardGpuStats publishes stats from gpuCh until it closes or ctx is done.
// It reports whether any stats were received.
func (m *Monitor) forwardGpuStats(ctx context.Context, gpuCh chan []GpuStat) bool {
	received := false
	for {
		select {
		case <-ctx.Done():
			return received
		case g, ok := <-gpuCh:
			if !ok {
				return received
			}
			received = true
			m.mutex.Lock()
			m.gpuRing.Push(g)
			for l := range m.gpuListeners {
				select {
				case l <- g:
				default:
				}
			}
			m.mutex.Unlock()
		}
	}
}

// Current returns a copy of the current log of system and GPU stats.
func (m *Monitor) Current() ([]SysStat, []GpuStat) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	sysStats := m.sysRing.Slice()

	snapshots := m.gpuRing.Slice()
	var gpuStats []GpuStat
	for _, snapshot := range snapshots {
		gpuStats = append(gpuStats, snapshot...)
	}
	return sysStats, gpuStats
}

func ReadSysStats() (SysStat, error) {
	return readSysStats()
}

func GetGpuStats(ctx context.Context, every time.Duration, logger *logmon.Monitor) (chan []GpuStat, error) {
	return getGpuStats(ctx, every, logger)
}
