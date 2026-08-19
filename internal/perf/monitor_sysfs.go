//go:build unix && !darwin

package perf

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
)

// The sysfs provider reads kernel interfaces directly so GPU stats work
// without vendor CLIs (nvidia-smi/rocm-smi) or the LACT daemon. It covers:
//
//   - any DRM card via hwmon: temperatures (by label), fan RPM/PWM, power
//     (power1_average or energy1_input deltas)
//   - amdgpu via sysfs: gpu_busy_percent and mem_info_vram_{used,total}
//   - everything else (Intel xe/i915, ...) via DRM fdinfo of processes
//     holding the render node: engine utilization (both the cycles-based
//     format drm-cycles-*/drm-total-cycles-* and the older
//     "drm-engine-<name>: N ns" format) and per-process VRAM
//     (drm-total-vram* / drm-resident-vram*), attributed per GPU via the
//     drm-pdev PCI address.
//
// Everything is unprivileged: hwmon and PCI resource files are world
// readable, and /proc/<pid>/fdinfo is readable for same-uid processes
// (llama-swap's own backends). Other processes fail open and are skipped.

var (
	sysfsRoot = "/sys"
	procRoot  = "/proc"
)

const minVramBarBytes = 1 << 30 // BARs >= 1 GiB count as VRAM

type sysfsGpu struct {
	id          int
	name        string
	uuid        string // PCI address
	sysDevPath  string // .../cardN/device
	driver      string
	hwmonPath   string // "" if none
	vramTotalMB int    // from largest PCI BAR; 0 = unknown
	amdgpu      bool

	// power state (energy counter deltas)
	lastEnergyUJ uint64
	lastEnergyAt time.Time

	// fdinfo engine state, keyed by drm-client-id -> engine -> sample
	fdState     map[string]map[string]fdEngineSample
	fdSampledAt time.Time
}

type fdEngineSample struct {
	cycles     uint64 // drm-cycles-<engine>
	totalCyc   uint64 // drm-total-cycles-<engine>
	engineNs   uint64 // drm-engine-<engine>: "<n> ns"
}

func trySysfs(ctx context.Context, every time.Duration, logger *logmon.Monitor) (chan []GpuStat, error) {
	gpus := discoverSysfsGpus()
	if len(gpus) == 0 {
		return nil, ErrNoGpuTool
	}

	ch := make(chan []GpuStat, 1)

	go func() {
		defer close(ch)
		ticker := time.NewTicker(every)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stats := make([]GpuStat, 0, len(gpus))
				for i := range gpus {
					if stat, err := gpus[i].poll(); err == nil {
						stats = append(stats, stat)
					}
				}
				if len(stats) > 0 {
					select {
					case ch <- stats:
					default:
					}
				}
			}
		}
	}()

	return ch, nil
}

func discoverSysfsGpus() []sysfsGpu {
	cards, err := filepath.Glob(filepath.Join(sysfsRoot, "class", "drm", "card[0-9]*"))
	if err != nil {
		return nil
	}
	sort.Strings(cards)

	var gpus []sysfsGpu
	for _, cardPath := range cards {
		if !isDigits(strings.TrimPrefix(filepath.Base(cardPath), "card")) {
			continue // card0-DP-1 etc.
		}
		devPath := filepath.Join(cardPath, "device")

		driver := readBaseName(filepath.Join(devPath, "driver"))
		if driver == "" {
			continue
		}

		hwmonPath := firstGlob(filepath.Join(devPath, "hwmon", "hwmon*"))
		amdgpu := driver == "amdgpu"
		if hwmonPath == "" && !amdgpu {
			continue // iGPU without sensors is not worth a panel
		}

		pciAddr := readBaseName(devPath) // device is a symlink to .../<addr>
		uuid := pciAddr
		if !strings.HasPrefix(pciAddr, "0000:") {
			uuid = filepath.Base(cardPath) // non-PCI (virtual) GPUs
		}

		g := sysfsGpu{
			id:         len(gpus),
			name:       gpuDisplayName(devPath, driver),
			uuid:       uuid,
			sysDevPath: devPath,
			driver:     driver,
			hwmonPath:  hwmonPath,
			amdgpu:     amdgpu,
		}
		if strings.HasPrefix(pciAddr, "0000:") {
			g.vramTotalMB = pciLargestBarMB(filepath.Join(sysfsRoot, "bus", "pci", "devices", pciAddr, "resource"))
		}
		gpus = append(gpus, g)
	}
	return gpus
}

func (g *sysfsGpu) poll() (GpuStat, error) {
	stat := GpuStat{
		Timestamp: time.Now(),
		ID:        g.id,
		Name:      g.name,
		UUID:      g.uuid,
	}

	// fdinfo first: reading /proc never wakes the GPU. It doubles as an
	// activity check -- hwmon/power reads go through the GPU's firmware
	// and wake a runtime-suspended card, so only touch them when a process
	// already holds the GPU (it is awake regardless). A sleeping GPU
	// reports zeroed telemetry, which is exactly the truth.
	active := g.readFdInfo(&stat)

	if active {
		g.readHwmon(&stat)
		if g.amdgpu {
			g.readAmdgpuSysfs(&stat)
		}
	}

	if stat.MemTotalMB == 0 {
		stat.MemTotalMB = g.vramTotalMB
	}
	if stat.MemTotalMB > 0 {
		stat.MemUtilPct = float64(stat.MemUsedMB) / float64(stat.MemTotalMB) * 100
	}

	return stat, nil
}

// readHwmon fills temps/fan/power from the card's hwmon node.
func (g *sysfsGpu) readHwmon(stat *GpuStat) {
	if g.hwmonPath == "" {
		return
	}

	type entry struct {
		label string
		tempC int
	}
	var temps []entry
	for i := 1; i <= 32; i++ {
		v, ok := readInt(filepath.Join(g.hwmonPath, "temp"+strconv.Itoa(i)+"_input"))
		if !ok || v <= 0 {
			continue
		}
		temps = append(temps, entry{
			label: strings.ToLower(readString(filepath.Join(g.hwmonPath, "temp"+strconv.Itoa(i)+"_label"))),
			tempC: int(v / 1000),
		})
	}
	for _, t := range temps {
		switch {
		case stat.TempC == 0 && labelMatches(t.label, "edge", "gpu", "pkg", "package"):
			stat.TempC = t.tempC
		case stat.VramTempC == 0 && labelMatches(t.label, "mem", "vram", "memory"):
			stat.VramTempC = t.tempC
		}
	}
	if stat.TempC == 0 && len(temps) > 0 {
		stat.TempC = temps[0].tempC
	}

	// fan: RPM against max if known, else raw PWM duty
	if fan, ok := readInt(filepath.Join(g.hwmonPath, "fan1_input")); ok && fan > 0 {
		if fanMax, ok := readInt(filepath.Join(g.hwmonPath, "fan1_max")); ok && fanMax > 0 {
			stat.FanSpeedPct = float64(fan) / float64(fanMax) * 100
		}
	}
	if stat.FanSpeedPct == 0 {
		if pwm, ok := readInt(filepath.Join(g.hwmonPath, "pwm1")); ok && pwm > 0 {
			stat.FanSpeedPct = float64(pwm) / 255 * 100
		}
	}

	// power: instant average if exposed (amdgpu, µW), else energy counter deltas (µJ)
	if avg, ok := readInt(filepath.Join(g.hwmonPath, "power1_average")); ok && avg > 0 {
		stat.PowerDrawW = float64(avg) / 1e6
	} else if energy, ok := readUint(filepath.Join(g.hwmonPath, "energy1_input")); ok {
		now := time.Now()
		if g.lastEnergyUJ > 0 && energy >= g.lastEnergyUJ && now.After(g.lastEnergyAt) {
			elapsed := now.Sub(g.lastEnergyAt).Seconds()
			if elapsed > 0 {
				stat.PowerDrawW = float64(energy-g.lastEnergyUJ) / elapsed / 1e6
			}
		}
		g.lastEnergyUJ, g.lastEnergyAt = energy, now
	}
}

// readAmdgpuSysfs fills util/vram from amdgpu-specific sysfs files.
func (g *sysfsGpu) readAmdgpuSysfs(stat *GpuStat) {
	if v, ok := readInt(filepath.Join(g.sysDevPath, "gpu_busy_percent")); ok {
		stat.GpuUtilPct = float64(v)
	}
	if v, ok := readInt(filepath.Join(g.sysDevPath, "mem_info_vram_total")); ok && v > 0 {
		stat.MemTotalMB = int(v / 1024 / 1024)
	}
	if v, ok := readInt(filepath.Join(g.sysDevPath, "mem_info_vram_used")); ok && v > 0 {
		stat.MemUsedMB = int(v / 1024 / 1024)
	}
}

// readFdInfo fills util/vram from DRM fdinfo of processes holding a render
// node that belongs to this GPU (matched via the drm-pdev PCI address).
// readFdInfo returns true when at least one process holds this GPU's render
// node (i.e. the GPU is in active use).
func (g *sysfsGpu) readFdInfo(stat *GpuStat) bool {
	if g.fdState == nil {
		g.fdState = map[string]map[string]fdEngineSample{}
	}
	now := time.Now()
	var elapsed float64
	if !g.fdSampledAt.IsZero() {
		elapsed = now.Sub(g.fdSampledAt).Seconds()
	}

	enginePct := map[string]float64{} // engine -> summed utilization %
	vramUsedKB := uint64(0)
	fdsFound := false

	procs, err := os.ReadDir(procRoot)
	if err != nil {
		return false
	}
	for _, p := range procs {
		pid := p.Name()
		if !isDigits(pid) {
			continue
		}
		fdDir := filepath.Join(procRoot, pid, "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue // not ours to read; fail open
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil || !strings.HasPrefix(target, "/dev/dri/renderD") {
				continue
			}
			kv := parseFdInfo(filepath.Join(procRoot, pid, "fdinfo", fd.Name()))
			if len(kv) == 0 {
				continue
			}
			if pdev, ok := kv["drm-pdev"]; ok && g.uuid != "" && pdev != g.uuid {
				continue // render node of a different GPU
			}
			fdsFound = true
			vramUsedKB += fdInfoVramKB(kv)
			fdInfoEnginePct(kv, g.fdState, elapsed, enginePct)
		}
	}

	if vramUsedKB > 0 {
		stat.MemUsedMB = int(vramUsedKB / 1024)
	}
	if len(enginePct) > 0 {
		best := 0.0
		for _, pct := range enginePct {
			if pct > best {
				best = pct
			}
		}
		if best > 100 {
			best = 100
		}
		stat.GpuUtilPct = best
	}
	g.fdSampledAt = now
	return fdsFound
}

// parseFdInfo reads a /proc/<pid>/fdinfo/<fd> file into key/value strings.
func parseFdInfo(path string) map[string]string {
	f, err := os.Open(path) // #nosec G304 -- /proc path built from ReadDir entries
	if err != nil {
		return nil
	}
	defer f.Close()

	kv := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if i := strings.IndexByte(line, ':'); i > 0 {
			kv[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
		}
	}
	return kv
}

// fdInfoVramKB sums allocated VRAM ("drm-total-vram*") reported in KiB,
// falling back to resident ("drm-resident-vram*").
func fdInfoVramKB(kv map[string]string) uint64 {
	var total, resident uint64
	for k, v := range kv {
		var dst *uint64
		switch {
		case strings.HasPrefix(k, "drm-total-vram"):
			dst = &total
		case strings.HasPrefix(k, "drm-resident-vram"):
			dst = &resident
		default:
			continue
		}
		if kb, ok := parseSizeKB(v); ok {
			*dst += kb
		}
	}
	if total > 0 {
		return total
	}
	return resident
}

// parseSizeKB parses fdinfo memory values ("N KiB", "N MiB", or raw bytes).
func parseSizeKB(v string) (uint64, bool) {
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return 0, false
	}
	n, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, false
	}
	if len(fields) == 1 {
		return n / 1024, true // bare value = bytes
	}
	switch strings.ToUpper(fields[1]) {
	case "B":
		return n / 1024, true
	case "KIB":
		return n, true
	case "MIB":
		return n * 1024, true
	case "GIB":
		return n * 1024 * 1024, true
	}
	return 0, false
}

// fdInfoEnginePct consumes one fdinfo sample, updates per-client engine
// state and accumulates per-engine utilization percentages into out.
// Handles both the cycles format and the older ns format.
func fdInfoEnginePct(kv map[string]string, state map[string]map[string]fdEngineSample, elapsed float64, out map[string]float64) {
	client := kv["drm-client-id"]
	if client == "" {
		return
	}

	cstate := state[client]
	if cstate == nil {
		cstate = map[string]fdEngineSample{}
		state[client] = cstate
	}

	// pass 1: gather this sample's engines
	type engSample struct {
		cur fdEngineSample
		ok  bool
	}
	engines := map[string]engSample{}

	for k, v := range kv {
		switch {
		case strings.HasPrefix(k, "drm-cycles-"):
			eng := strings.TrimPrefix(k, "drm-cycles-")
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				e := engines[eng]
				e.cur.cycles = n
				e.ok = true
				engines[eng] = e
			}
		case strings.HasPrefix(k, "drm-total-cycles-"):
			eng := strings.TrimPrefix(k, "drm-total-cycles-")
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				e := engines[eng]
				e.cur.totalCyc = n
				e.ok = true
				engines[eng] = e
			}
		case strings.HasPrefix(k, "drm-engine-") && strings.HasSuffix(v, "ns"):
			eng := strings.TrimPrefix(k, "drm-engine-")
			if n, err := strconv.ParseUint(strings.TrimSuffix(strings.Fields(v)[0], "ns"), 10, 64); err == nil {
				e := engines[eng]
				e.cur.engineNs = n
				e.ok = true
				engines[eng] = e
			}
		}
	}

	// pass 2: compute deltas against previous sample
	for eng, s := range engines {
		if !s.ok {
			continue
		}
		prev, had := cstate[eng]
		cstate[eng] = s.cur

		if !had {
			continue // first sample only seeds state
		}
		if s.cur.totalCyc > prev.totalCyc && s.cur.cycles >= prev.cycles {
			pct := float64(s.cur.cycles-prev.cycles) / float64(s.cur.totalCyc-prev.totalCyc) * 100
			out[eng] += pct
		} else if s.cur.engineNs >= prev.engineNs && elapsed > 0 {
			pct := float64(s.cur.engineNs-prev.engineNs) / (elapsed * 1e9) * 100
			out[eng] += pct
		}
	}
}

// -- small helpers -----------------------------------------------------------

func gpuDisplayName(devPath, driver string) string {
	vendor := strings.ToLower(readString(filepath.Join(devPath, "vendor")))
	device := strings.ToLower(readString(filepath.Join(devPath, "device")))

	brand := "GPU"
	switch vendor {
	case "0x8086":
		brand = "Intel"
	case "0x1002":
		brand = "AMD"
	case "0x10de":
		brand = "NVIDIA"
	}

	if device != "" {
		return brand + " " + driver + " " + device
	}
	return brand + " " + driver
}

// pciLargestBarMB returns the largest PCI BAR size >= 1 GiB (the VRAM BAR
// on discrete cards), or 0.
func pciLargestBarMB(resourcePath string) int {
	f, err := os.Open(resourcePath) // #nosec G304 -- path under sysfsRoot
	if err != nil {
		return 0
	}
	defer f.Close()

	best := uint64(0)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		start, err1 := strconv.ParseUint(fields[0], 0, 64)
		end, err2 := strconv.ParseUint(fields[1], 0, 64)
		if err1 != nil || err2 != nil || end <= start || start == 0 {
			continue
		}
		if size := end - start + 1; size > best {
			best = size
		}
	}
	if best >= minVramBarBytes {
		return int(best / 1024 / 1024)
	}
	return 0
}

func labelMatches(label string, candidates ...string) bool {
	if label == "" {
		return false
	}
	for _, c := range candidates {
		if label == c || strings.HasPrefix(label, c) {
			return true
		}
	}
	return false
}

func readBaseName(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return filepath.Base(resolved)
}

func readString(path string) string {
	b, err := os.ReadFile(path) // #nosec G304 -- paths built from sysfsRoot
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readInt(path string) (int64, bool) {
	s := readString(path)
	if s == "" {
		return 0, false
	}
	// tolerate trailing units on hwmon values
	if i := strings.IndexByte(s, ' '); i > 0 {
		s = s[:i]
	}
	n, err := strconv.ParseInt(s, 0, 64)
	return n, err == nil
}

func readUint(path string) (uint64, bool) {
	s := readString(path)
	if s == "" {
		return 0, false
	}
	if i := strings.IndexByte(s, ' '); i > 0 {
		s = s[:i]
	}
	n, err := strconv.ParseUint(s, 0, 64)
	return n, err == nil
}

func firstGlob(pattern string) string {
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[0]
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
