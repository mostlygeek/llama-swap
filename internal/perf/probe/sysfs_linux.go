package probe

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// procRoot is the procfs mount point. Tests point it at a fixture tree.
var procRoot = "/proc"

// minVramBarBytes is the smallest PCI BAR counted as VRAM.
const minVramBarBytes = 1 << 30

// sysfs reads GPUs straight from kernel interfaces, so it works without
// vendor CLIs or the LACT daemon:
//
//   - hwmon on any DRM card: temperatures by label, fan, power
//     (power1_average, or energy1_input deltas)
//   - amdgpu counters: gpu_busy_percent, mem_info_vram_*, mem_info_gtt_*
//   - DRM fdinfo of processes holding a render node, for engine utilization
//     and VRAM on other drivers (Intel xe/i915, ...)
//
// Everything is unprivileged. /proc/<pid>/fdinfo is only readable for
// same-user processes, which covers llama-swap's own backends.
type sysfs struct {
	skipSuspended bool

	mu    sync.Mutex
	state map[string]*sysfsCardState // by GPU key
}

// sysfsCardState carries counters between reads to compute rates.
type sysfsCardState struct {
	lastEnergyUJ uint64
	lastEnergyAt time.Time
	fdEngines    map[string]map[string]fdEngineSample // drm-client-id -> engine
	fdSampledAt  time.Time
}

type fdEngineSample struct {
	cycles   uint64 // drm-cycles-<engine>
	totalCyc uint64 // drm-total-cycles-<engine>
	engineNs uint64 // drm-engine-<engine>: "<n> ns"
}

// sysfsCard is a DRM card found in sysfs.
type sysfsCard struct {
	key     string
	pci     string // "" for non-PCI cards
	name    string
	devPath string // .../cardN/device
	driver  string
	hwmon   string // "" if none
}

func newSysfs(opts Options) *sysfs {
	return &sysfs{skipSuspended: opts.SkipSuspended, state: map[string]*sysfsCardState{}}
}

func (p *sysfs) Name() string { return "sysfs" }

func (p *sysfs) Read(ctx context.Context) ([]GpuSample, error) {
	cards := discoverSysfsCards()
	if len(cards) == 0 {
		return nil, fmt.Errorf("sysfs: no DRM cards with sensors: %w", ErrUnavailable)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	fd := readFdInfoRecords(len(cards) > 1)
	samples := make([]GpuSample, 0, len(cards))
	for _, c := range cards {
		if p.skipSuspended && runtimeSuspended(c.devPath) {
			samples = append(samples, GpuSample{Key: c.key, Name: c.name, State: GpuStateSuspended})
			continue
		}
		st := p.state[c.key]
		if st == nil {
			st = &sysfsCardState{}
			p.state[c.key] = st
		}
		records := fd[c.pci]
		if len(cards) == 1 && c.pci != "" {
			// with one card, unattributed records can only be for it
			records = append(records, fd[""]...)
		}
		samples = append(samples, c.read(st, records))
	}
	return samples, nil
}

func discoverSysfsCards() []sysfsCard {
	paths, err := filepath.Glob(filepath.Join(sysfsRoot, "class", "drm", "card[0-9]*"))
	if err != nil {
		return nil
	}
	sort.Strings(paths)

	var cards []sysfsCard
	for _, cardPath := range paths {
		if !isDigits(strings.TrimPrefix(filepath.Base(cardPath), "card")) {
			continue // connectors such as card0-DP-1
		}
		devPath := filepath.Join(cardPath, "device")
		driver := readBaseName(filepath.Join(devPath, "driver"))
		if driver == "" {
			continue
		}
		hwmon := firstGlob(filepath.Join(devPath, "hwmon", "hwmon*"))
		if hwmon == "" && driver != "amdgpu" {
			continue // an iGPU without sensors is not worth a panel
		}
		pci := NormalizePCI(readBaseName(devPath))
		key := pci
		if key == "" {
			key = fallbackKey("sysfs", len(cards))
		}
		cards = append(cards, sysfsCard{
			key:     key,
			pci:     pci,
			name:    sysfsGpuName(devPath, driver),
			devPath: devPath,
			driver:  driver,
			hwmon:   hwmon,
		})
	}
	return cards
}

func (c sysfsCard) read(st *sysfsCardState, fd []fdInfoRecord) GpuSample {
	s := GpuSample{Key: c.key, Name: c.name, State: GpuStateOK}
	c.readHwmon(st, &s)
	if c.driver == "amdgpu" {
		c.readAmdgpu(&s)
	}
	// fdinfo fills in utilization and VRAM for drivers without counters.
	util, vramMB := fdInfoUsage(st, fd)
	if s.UtilPct == nil {
		s.UtilPct = util
	}
	if s.VramUsedMB == nil {
		s.VramUsedMB = vramMB
	}
	if s.VramTotalMB == nil && c.pci != "" {
		s.VramTotalMB = pciLargestBarMB(filepath.Join(sysfsRoot, "bus", "pci", "devices", c.pci, "resource"))
	}
	return s
}

func (c sysfsCard) readHwmon(st *sysfsCardState, s *GpuSample) {
	if c.hwmon == "" {
		return
	}
	var first *float64
	for i := 1; i <= 32; i++ {
		v, ok := readInt(filepath.Join(c.hwmon, "temp"+strconv.Itoa(i)+"_input"))
		if !ok || v <= 0 {
			continue
		}
		t := ptr(float64(v) / 1000)
		if first == nil {
			first = t
		}
		label := strings.ToLower(readString(filepath.Join(c.hwmon, "temp"+strconv.Itoa(i)+"_label")))
		switch {
		case s.TempC == nil && labelMatches(label, "edge", "gpu", "pkg", "package"):
			s.TempC = t
		case s.VramTempC == nil && labelMatches(label, "mem", "vram", "memory"):
			s.VramTempC = t
		}
	}
	if s.TempC == nil {
		s.TempC = first
	}

	// Fan: RPM against max when known, else the PWM duty cycle.
	if fan, ok := readInt(filepath.Join(c.hwmon, "fan1_input")); ok {
		if fanMax, ok := readInt(filepath.Join(c.hwmon, "fan1_max")); ok && fanMax > 0 {
			s.FanPct = ptr(float64(fan) / float64(fanMax) * 100)
		}
	}
	if s.FanPct == nil {
		if pwm, ok := readInt(filepath.Join(c.hwmon, "pwm1")); ok {
			s.FanPct = ptr(float64(pwm) / 255 * 100)
		}
	}

	// Power: the driver's average (µW) when exposed, else energy deltas (µJ).
	if avg, ok := readInt(filepath.Join(c.hwmon, "power1_average")); ok && avg > 0 {
		s.PowerW = ptr(float64(avg) / 1e6)
	} else if energy, ok := readUint(filepath.Join(c.hwmon, "energy1_input")); ok {
		now := time.Now()
		if st.lastEnergyUJ > 0 && energy >= st.lastEnergyUJ && now.After(st.lastEnergyAt) {
			s.PowerW = ptr(float64(energy-st.lastEnergyUJ) / now.Sub(st.lastEnergyAt).Seconds() / 1e6)
		}
		st.lastEnergyUJ, st.lastEnergyAt = energy, now
	}
}

// readAmdgpu reads amdgpu's counters. GTT is system RAM the GPU uses, which
// is where an APU keeps the model.
func (c sysfsCard) readAmdgpu(s *GpuSample) {
	if v, ok := readInt(filepath.Join(c.devPath, "gpu_busy_percent")); ok {
		s.UtilPct = ptr(float64(v))
	}
	mb := func(file string) *int {
		if v, ok := readInt(filepath.Join(c.devPath, file)); ok && v >= 0 {
			return ptr(int(v / bytesPerMB))
		}
		return nil
	}
	s.VramTotalMB = mb("mem_info_vram_total")
	s.VramUsedMB = mb("mem_info_vram_used")
	s.SharedTotalMB = mb("mem_info_gtt_total")
	s.SharedUsedMB = mb("mem_info_gtt_used")
}

// fdInfoRecord is one DRM client seen in /proc/<pid>/fdinfo.
type fdInfoRecord map[string]string

// readFdInfoRecords collects DRM fdinfo records by the PCI address of the
// card they belong to, deduplicated by drm-client-id. Records that cannot be
// tied to a card go under "". With ambiguous set (several cards) they are
// dropped instead, rather than counted on every card.
func readFdInfoRecords(ambiguous bool) map[string][]fdInfoRecord {
	procs, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	byCard := map[string][]fdInfoRecord{}
	seen := map[string]bool{} // pci/drm-client-id
	renderPCI := map[string]string{}
	for _, proc := range procs {
		if !isDigits(proc.Name()) {
			continue
		}
		fdDir := filepath.Join(procRoot, proc.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue // not ours to read
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil || !strings.HasPrefix(target, "/dev/dri/renderD") {
				continue
			}
			kv := parseFdInfo(filepath.Join(procRoot, proc.Name(), "fdinfo", fd.Name()))
			if len(kv) == 0 {
				continue
			}
			// drm-pdev is the kernel's answer on newer kernels; otherwise
			// resolve the render node through sysfs.
			pci := NormalizePCI(kv["drm-pdev"])
			if pci == "" {
				node := filepath.Base(target)
				if _, ok := renderPCI[node]; !ok {
					renderPCI[node] = NormalizePCI(readBaseName(filepath.Join(sysfsRoot, "class", "drm", node, "device")))
				}
				pci = renderPCI[node]
			}
			if pci == "" && ambiguous {
				continue
			}
			// Duplicated or inherited fds share one client id.
			if client := kv["drm-client-id"]; client != "" {
				if seen[pci+"/"+client] {
					continue
				}
				seen[pci+"/"+client] = true
			}
			byCard[pci] = append(byCard[pci], kv)
		}
	}
	return byCard
}

// fdInfoUsage turns a card's fdinfo records into utilization (the busiest
// engine) and VRAM used. Utilization needs two reads, so the first is nil.
func fdInfoUsage(st *sysfsCardState, records []fdInfoRecord) (util *float64, vramMB *int) {
	if st.fdEngines == nil {
		st.fdEngines = map[string]map[string]fdEngineSample{}
	}
	now := time.Now()
	var elapsed float64
	if !st.fdSampledAt.IsZero() {
		elapsed = now.Sub(st.fdSampledAt).Seconds()
	}
	st.fdSampledAt = now

	engines := map[string]float64{}
	var vramKB uint64
	for _, kv := range records {
		vramKB += fdInfoVramKB(kv)
		fdInfoEnginePct(kv, st.fdEngines, elapsed, engines)
	}
	if vramKB > 0 {
		vramMB = ptr(int(vramKB / 1024))
	}
	if len(engines) > 0 {
		best := 0.0
		for _, pct := range engines {
			best = max(best, pct)
		}
		util = ptr(min(best, 100))
	}
	return util, vramMB
}

func parseFdInfo(path string) fdInfoRecord {
	f, err := os.Open(path) // #nosec G304 -- /proc path built from ReadDir entries
	if err != nil {
		return nil
	}
	defer f.Close()

	kv := fdInfoRecord{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if k, v, ok := strings.Cut(scanner.Text(), ":"); ok {
			kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return kv
}

// fdInfoVramKB sums allocated VRAM (drm-total-vram*), falling back to
// resident (drm-resident-vram*).
func fdInfoVramKB(kv fdInfoRecord) uint64 {
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

// parseSizeKB parses fdinfo memory values: "N KiB", "N MiB", or bytes.
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
		return n / 1024, true
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

// fdInfoEnginePct adds one client's per-engine utilization since its last
// sample to out. It handles the cycles format and the older ns format.
func fdInfoEnginePct(kv fdInfoRecord, state map[string]map[string]fdEngineSample, elapsed float64, out map[string]float64) {
	client := kv["drm-client-id"]
	if client == "" {
		return
	}
	prev := state[client]
	if prev == nil {
		prev = map[string]fdEngineSample{}
		state[client] = prev
	}

	cur := map[string]fdEngineSample{}
	for k, v := range kv {
		switch {
		case strings.HasPrefix(k, "drm-cycles-"):
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				e := cur[strings.TrimPrefix(k, "drm-cycles-")]
				e.cycles = n
				cur[strings.TrimPrefix(k, "drm-cycles-")] = e
			}
		case strings.HasPrefix(k, "drm-total-cycles-"):
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				e := cur[strings.TrimPrefix(k, "drm-total-cycles-")]
				e.totalCyc = n
				cur[strings.TrimPrefix(k, "drm-total-cycles-")] = e
			}
		case strings.HasPrefix(k, "drm-engine-") && strings.HasSuffix(v, "ns"):
			if n, err := strconv.ParseUint(strings.Fields(v)[0], 10, 64); err == nil {
				e := cur[strings.TrimPrefix(k, "drm-engine-")]
				e.engineNs = n
				cur[strings.TrimPrefix(k, "drm-engine-")] = e
			}
		}
	}

	for eng, c := range cur {
		p, had := prev[eng]
		prev[eng] = c
		if !had {
			continue // the first sample only seeds state
		}
		if c.totalCyc > p.totalCyc && c.cycles >= p.cycles {
			out[eng] += float64(c.cycles-p.cycles) / float64(c.totalCyc-p.totalCyc) * 100
		} else if c.engineNs >= p.engineNs && elapsed > 0 {
			out[eng] += float64(c.engineNs-p.engineNs) / (elapsed * 1e9) * 100
		}
	}
}

func sysfsGpuName(devPath, driver string) string {
	brand := "GPU"
	switch strings.ToLower(readString(filepath.Join(devPath, "vendor"))) {
	case vendorIntel:
		brand = "Intel"
	case vendorAMD:
		brand = "AMD"
	case vendorNVIDIA:
		brand = "NVIDIA"
	}
	if device := strings.ToLower(readString(filepath.Join(devPath, "device"))); device != "" {
		return brand + " " + driver + " " + device
	}
	return brand + " " + driver
}

// pciLargestBarMB returns the largest PCI BAR of at least 1 GiB (the VRAM BAR
// on discrete cards), or nil.
func pciLargestBarMB(resourcePath string) *int {
	f, err := os.Open(resourcePath) // #nosec G304 -- path under sysfsRoot
	if err != nil {
		return nil
	}
	defer f.Close()

	var best uint64
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
		best = max(best, end-start+1)
	}
	if best >= minVramBarBytes {
		return ptr(int(best / bytesPerMB))
	}
	return nil
}

func labelMatches(label string, candidates ...string) bool {
	if label == "" {
		return false
	}
	for _, c := range candidates {
		if strings.HasPrefix(label, c) {
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

func readInt(path string) (int64, bool) {
	s := readString(path)
	if i := strings.IndexByte(s, ' '); i > 0 {
		s = s[:i] // tolerate trailing units
	}
	n, err := strconv.ParseInt(s, 0, 64)
	return n, err == nil
}

func readUint(path string) (uint64, bool) {
	s := readString(path)
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
