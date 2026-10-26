//go:build windows

package probe

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	d3dkmDLL                *windows.LazyDLL
	procEnumAdapters2       *windows.LazyProc
	procOpenAdapterFromLuid *windows.LazyProc
	procCloseAdapter        *windows.LazyProc
	procQueryAdapterInfo    *windows.LazyProc
	procQueryStatistics     *windows.LazyProc
	d3dkmtInitOnce          sync.Once
	d3dkmtInitErr           error
)

// initD3DKMT lazily loads gdi32.dll and resolves D3DKMT function pointers.
// Safe for concurrent use via sync.Once.
func initD3DKMT() error {
	d3dkmtInitOnce.Do(func() {
		d3dkmDLL = windows.NewLazySystemDLL("gdi32.dll")

		procEnumAdapters2 = d3dkmDLL.NewProc("D3DKMTEnumAdapters2")
		procOpenAdapterFromLuid = d3dkmDLL.NewProc("D3DKMTOpenAdapterFromLuid")
		procCloseAdapter = d3dkmDLL.NewProc("D3DKMTCloseAdapter")
		procQueryAdapterInfo = d3dkmDLL.NewProc("D3DKMTQueryAdapterInfo")
		procQueryStatistics = d3dkmDLL.NewProc("D3DKMTQueryStatistics")

		for name, p := range map[string]*windows.LazyProc{
			"D3DKMTEnumAdapters2":       procEnumAdapters2,
			"D3DKMTOpenAdapterFromLuid": procOpenAdapterFromLuid,
			"D3DKMTCloseAdapter":        procCloseAdapter,
			"D3DKMTQueryAdapterInfo":    procQueryAdapterInfo,
			"D3DKMTQueryStatistics":     procQueryStatistics,
		} {
			if err := p.Find(); err != nil {
				d3dkmtInitErr = fmt.Errorf("D3DKMT %s not found: %w", name, err)
				return
			}
		}
	})
	return d3dkmtInitErr
}

// ntstatusCall invokes a D3DKMT function and returns a non-nil error if the
// NTSTATUS result is not STATUS_SUCCESS (0).
func ntstatusCall(proc *windows.LazyProc, arg unsafe.Pointer) error {
	ret, _, _ := proc.Call(uintptr(arg))
	if ret != 0 {
		return fmt.Errorf("NTSTATUS 0x%08x", uint32(ret))
	}
	return nil
}

// d3dkmEnumerateAdapters enumerates all available graphics adapters via
// D3DKMTEnumAdapters2.
func d3dkmEnumerateAdapters() ([]D3DKMT_ADAPTERINFO, error) {
	var adapters [maxEnumAdapters]D3DKMT_ADAPTERINFO
	enum := D3DKMT_ENUMADAPTERS2{
		NumAdapters: maxEnumAdapters,
		pAdapters:   uintptr(unsafe.Pointer(&adapters[0])),
	}
	if err := ntstatusCall(procEnumAdapters2, unsafe.Pointer(&enum)); err != nil {
		return nil, fmt.Errorf("EnumAdapters2: %w", err)
	}
	if enum.NumAdapters == 0 {
		return nil, fmt.Errorf("no adapters found")
	}
	result := make([]D3DKMT_ADAPTERINFO, enum.NumAdapters)
	for i := uint32(0); i < enum.NumAdapters; i++ {
		result[i] = adapters[i]
	}
	return result, nil
}

// d3dkmOpenAdapter opens a D3DKMT adapter handle for the given LUID.
func d3dkmOpenAdapter(luid LUID) (uint32, error) {
	req := D3DKMT_OPENADAPTERFROMLUID{
		AdapterLuid: luid,
	}
	if err := ntstatusCall(procOpenAdapterFromLuid, unsafe.Pointer(&req)); err != nil {
		return 0, fmt.Errorf("OpenAdapterFromLuid: %w", err)
	}
	return req.hAdapter, nil
}

// d3dkmCloseAdapter closes a previously opened D3DKMT adapter handle.
func d3dkmCloseAdapter(hAdapter uint32) error {
	req := D3DKMT_CLOSEADAPTER{hAdapter: hAdapter}
	return ntstatusCall(procCloseAdapter, unsafe.Pointer(&req))
}

// d3dkmGetAdapterPerfData queries per-adapter performance data (temperature,
// fan RPM, power, bandwidth) via KMTQAITYPE_ADAPTERPERFDATA.
func d3dkmGetAdapterPerfData(hAdapter uint32) (*D3DKMT_ADAPTER_PERFDATA, error) {
	var data D3DKMT_ADAPTER_PERFDATA
	req := D3DKMT_QUERYADAPTERINFO{
		hAdapter:              hAdapter,
		Type:                  KMTQAITYPE_ADAPTERPERFDATA,
		pPrivateDriverData:    uintptr(unsafe.Pointer(&data)),
		PrivateDriverDataSize: uint32(unsafe.Sizeof(data)),
	}
	if err := ntstatusCall(procQueryAdapterInfo, unsafe.Pointer(&req)); err != nil {
		return nil, fmt.Errorf("QueryAdapterInfo(ADAPTERPERFDATA): %w", err)
	}
	return &data, nil
}

// d3dkmGetAdapterPerfDataCaps queries static adapter performance capabilities
// (max fan RPM, temperature limits, max bandwidth) via KMTQAITYPE_ADAPTERPERFDATA_CAPS.
func d3dkmGetAdapterPerfDataCaps(hAdapter uint32) (*D3DKMT_ADAPTER_PERFDATACAPS, error) {
	var data D3DKMT_ADAPTER_PERFDATACAPS
	req := D3DKMT_QUERYADAPTERINFO{
		hAdapter:              hAdapter,
		Type:                  KMTQAITYPE_ADAPTERPERFDATA_CAPS,
		pPrivateDriverData:    uintptr(unsafe.Pointer(&data)),
		PrivateDriverDataSize: uint32(unsafe.Sizeof(data)),
	}
	if err := ntstatusCall(procQueryAdapterInfo, unsafe.Pointer(&req)); err != nil {
		return nil, fmt.Errorf("QueryAdapterInfo(ADAPTERPERFDATACAPS): %w", err)
	}
	return &data, nil
}

type queryStatsBuffer struct {
	Type        int32   // offset 0
	AdapterLuid LUID    // offset 4
	hProcess    uintptr // offset 16
	// _result mirrors the D3DKMT_QUERYSTATISTICS_RESULT union.
	// sizeof(D3DKMT_QUERYSTATISTICS) == 0x328 (808 bytes) on x64.
	//
	// The C struct layout (x64):
	//   offset  0: Type (int32, 4 bytes)
	//   offset  4: AdapterLuid (LUID, 8 bytes)
	//   offset 12: 4 bytes padding (for 8-byte alignment of hProcess)
	//   offset 16: hProcess (HANDLE, 8 bytes)
	//   offset 24: QueryResult (union, 780 bytes — largest member is AdapterInformation)
	//   offset 804: anonymous input union (QueryNode.NodeId / QuerySegment.SegmentId, 4 bytes)
	//
	// Previous bug: _result was [776]byte, placing QueryId at offset 800 instead of 804.
	// The kernel read NodeId/SegmentId from offset 804 (always zero from _pad),
	// causing all NODE and SEGMENT queries to use index 0 regardless of the value
	// passed in QueryId. This produced alternating behavior where only GPU util OR
	// memory util appeared to work, depending on which test variant happened to put
	// non-zero data near offset 804 in the result buffer.
	_result [780]byte // offset 24, size 780 — places QueryId at offset 804
	QueryId int32     // offset 804 — matches C anonymous union for NodeId/SegmentId
}

func init() {
	var buf queryStatsBuffer
	if unsafe.Sizeof(buf) != 808 {
		panic(fmt.Sprintf("queryStatsBuffer size %d != expected 808 (sizeof D3DKMT_QUERYSTATISTICS on x64)", unsafe.Sizeof(buf)))
	}
	if unsafe.Offsetof(buf.QueryId) != 804 {
		panic(fmt.Sprintf("queryStatsBuffer.QueryId offset %d != expected 804 (C anonymous union offset)", unsafe.Offsetof(buf.QueryId)))
	}

	var perfData D3DKMT_ADAPTER_PERFDATA
	if unsafe.Sizeof(perfData) != 64 {
		panic(fmt.Sprintf("D3DKMT_ADAPTER_PERFDATA size %d != expected 64 on x64", unsafe.Sizeof(perfData)))
	}

	var caps D3DKMT_ADAPTER_PERFDATACAPS
	if unsafe.Sizeof(caps) != 40 {
		panic(fmt.Sprintf("D3DKMT_ADAPTER_PERFDATACAPS size %d != expected 40 on x64", unsafe.Sizeof(caps)))
	}
}

const (
	qsoffsetNbSegments        = 0
	qsoffsetNodeCount         = 4
	qsoffsetCommitLimit       = 0
	qsoffsetBytesCommitted    = 8
	qsoffsetBytesResident     = 16
	qsoffsetRunningTime       = 0
	qsoffsetSystemRunningTime = 272
)

// d3dkmQueryAdapterStats returns the number of memory segments and compute
// nodes for the adapter identified by luid.
func d3dkmQueryAdapterStats(luid LUID) (nbSegments uint32, nodeCount uint32, err error) {
	buf := queryStatsBuffer{
		Type:        int32(D3DKMT_QUERYSTATISTICS_ADAPTER),
		AdapterLuid: luid,
	}
	if err := ntstatusCall(procQueryStatistics, unsafe.Pointer(&buf)); err != nil {
		return 0, 0, fmt.Errorf("QueryStatistics(ADAPTER): %w", err)
	}
	nbSegments = binary.LittleEndian.Uint32(buf._result[qsoffsetNbSegments : qsoffsetNbSegments+4])
	nodeCount = binary.LittleEndian.Uint32(buf._result[qsoffsetNodeCount : qsoffsetNodeCount+4])
	return nbSegments, nodeCount, nil
}

// d3dkmQuerySegmentStats returns the commit limit (total) and resident
// (used) bytes for the given memory segment of an adapter.
func d3dkmQuerySegmentStats(luid LUID, segmentID uint32) (commitLimit uint64, bytesResident uint64, err error) {
	buf := queryStatsBuffer{
		Type:        int32(D3DKMT_QUERYSTATISTICS_SEGMENT),
		AdapterLuid: luid,
		QueryId:     int32(segmentID),
	}
	if err := ntstatusCall(procQueryStatistics, unsafe.Pointer(&buf)); err != nil {
		return 0, 0, fmt.Errorf("QueryStatistics(SEGMENT %d): %w", segmentID, err)
	}
	commitLimit = binary.LittleEndian.Uint64(buf._result[qsoffsetCommitLimit : qsoffsetCommitLimit+8])
	bytesResident = binary.LittleEndian.Uint64(buf._result[qsoffsetBytesResident : qsoffsetBytesResident+8])
	if bytesResident == 0 {
		bytesResident = binary.LittleEndian.Uint64(buf._result[qsoffsetBytesCommitted : qsoffsetBytesCommitted+8])
	}
	return commitLimit, bytesResident, nil
}

// d3dkmQueryNodeStats returns the global and system running time counters
// (in 100ns units) for the given compute node of an adapter.
func d3dkmQueryNodeStats(luid LUID, nodeID uint32) (runningTime uint64, systemRunningTime uint64, err error) {
	buf := queryStatsBuffer{
		Type:        int32(D3DKMT_QUERYSTATISTICS_NODE),
		AdapterLuid: luid,
		QueryId:     int32(nodeID),
	}
	if err := ntstatusCall(procQueryStatistics, unsafe.Pointer(&buf)); err != nil {
		return 0, 0, fmt.Errorf("QueryStatistics(NODE %d): %w", nodeID, err)
	}
	runningTime = binary.LittleEndian.Uint64(buf._result[qsoffsetRunningTime : qsoffsetRunningTime+8])
	systemRunningTime = binary.LittleEndian.Uint64(buf._result[qsoffsetSystemRunningTime : qsoffsetSystemRunningTime+8])
	return runningTime, systemRunningTime, nil
}

type nodeRunningTimes struct {
	Global uint64
	System uint64
}

// d3dkmtNodeUtil computes GPU node utilization as a percentage from running
// time deltas. Returns -1 if counters went backwards (wrap/reset), 0 if idle.
func d3dkmtNodeUtil(prevRT, curRT nodeRunningTimes, elapsed100ns int64) float64 {
	if curRT.Global < prevRT.Global || curRT.System < prevRT.System {
		return -1
	}
	gd := curRT.Global - prevRT.Global
	sd := curRT.System - prevRT.System

	if gd > 0 && sd > 0 {
		util := float64(gd) / float64(sd)
		if util > 1.0 {
			util = 1.0
		}
		return util * 100.0
	} else if gd > 0 && elapsed100ns > 0 {
		util := float64(gd) / float64(elapsed100ns) * 100.0
		if util > 100.0 {
			util = 100.0
		}
		return util
	}
	return 0
}

// d3dkmtFanPct returns fan speed as a percentage of maxFanRPM, clamped to
// 100%. Returns 0 if maxFanRPM is unavailable or fan is not spinning.
func d3dkmtFanPct(fanRPM, maxFanRPM uint32) float64 {
	if maxFanRPM > 0 && fanRPM > 0 {
		pct := float64(fanRPM) / float64(maxFanRPM) * 100.0
		if pct > 100.0 {
			pct = 100.0
		}
		return pct
	}
	return 0
}

// d3dkmtPowerW converts power from deci-watts (as reported by D3DKMT) to
// watts. Returns 0 if the power value is zero.
func d3dkmtPowerW(power uint32) float64 {
	if power > 0 {
		return float64(power) / 10.0
	}
	return 0
}

// d3dkmtTempC converts temperature from deci-Celsius (as reported by D3DKMT)
// to degrees Celsius.
func d3dkmtTempC(tempDeciC uint32) int {
	return int(tempDeciC / 10)
}

// d3dkmtAdapterAddress returns the adapter's PCI address as a key, or "" for
// adapters without one, such as the Microsoft Basic Render Driver.
func d3dkmtAdapterAddress(hAdapter uint32) string {
	var addr D3DKMT_ADAPTERADDRESS
	req := D3DKMT_QUERYADAPTERINFO{
		hAdapter:              hAdapter,
		Type:                  KMTQAITYPE_ADAPTERADDRESS,
		pPrivateDriverData:    uintptr(unsafe.Pointer(&addr)),
		PrivateDriverDataSize: uint32(unsafe.Sizeof(addr)),
	}
	if err := ntstatusCall(procQueryAdapterInfo, unsafe.Pointer(&req)); err != nil {
		return ""
	}
	// D3DKMT reports no PCI domain; Windows GPUs sit in domain 0.
	return FormatPCI(0, addr.BusNumber, addr.DeviceNumber, addr.FunctionNumber)
}

// d3dkmt reads any WDDM GPU through the Windows kernel graphics interface,
// with PDH counters for utilization when available.
type d3dkmt struct {
	mu      sync.Mutex
	prev    map[LUID]map[uint32]nodeRunningTimes
	prevAt  map[LUID]time.Time
	pdh     *pdhGpuUtil
	pdhDone bool
}

func newD3dkmt() *d3dkmt {
	return &d3dkmt{prev: map[LUID]map[uint32]nodeRunningTimes{}, prevAt: map[LUID]time.Time{}}
}

func (p *d3dkmt) Name() string { return "d3dkmt" }

// Close releases the PDH query.
func (p *d3dkmt) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pdh != nil {
		p.pdh.close()
		p.pdh = nil
	}
	return nil
}

func (p *d3dkmt) Read(ctx context.Context) ([]GpuSample, error) {
	if err := initD3DKMT(); err != nil {
		return nil, fmt.Errorf("d3dkmt: %w: %w", ErrUnavailable, err)
	}
	adapters, err := d3dkmEnumerateAdapters()
	if err != nil {
		return nil, fmt.Errorf("d3dkmt: %w: %w", ErrUnavailable, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.pdhDone {
		p.pdhDone = true
		if pdh, err := initPdhGpuUtil(); err == nil {
			p.pdh = pdh
		}
	}
	var pdhUtil map[LUID]float64
	if p.pdh != nil {
		pdhUtil = p.pdh.collect()
	}

	samples := make([]GpuSample, 0, len(adapters))
	for i, ai := range adapters {
		if s, ok := p.readAdapter(i, ai.AdapterLuid, pdhUtil); ok {
			samples = append(samples, s)
		}
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("d3dkmt: no usable adapters: %w", ErrUnavailable)
	}
	return samples, nil
}

func (p *d3dkmt) readAdapter(index int, luid LUID, pdhUtil map[LUID]float64) (GpuSample, bool) {
	hAdapter, err := d3dkmOpenAdapter(luid)
	if err != nil {
		return GpuSample{}, false
	}
	defer d3dkmCloseAdapter(hAdapter)

	nbSegments, nodeCount, err := d3dkmQueryAdapterStats(luid)
	if err != nil {
		return GpuSample{}, false
	}

	s := GpuSample{
		Key:   d3dkmtAdapterAddress(hAdapter),
		Name:  fmt.Sprintf("GPU %d", index),
		State: GpuStateOK,
	}
	if s.Key == "" {
		s.Key = fallbackKey("d3dkmt", index)
	}

	var maxFanRPM uint32
	if caps, err := d3dkmGetAdapterPerfDataCaps(hAdapter); err == nil {
		maxFanRPM = caps.MaxFanRPM
	}
	if perf, err := d3dkmGetAdapterPerfData(hAdapter); err == nil {
		// Drivers that don't fill a field leave it at 0, so 0 means not
		// reported (#1171: AMD leaves Power empty).
		if perf.Temperature > 0 {
			s.TempC = ptr(float64(d3dkmtTempC(perf.Temperature)))
		}
		if pct := d3dkmtFanPct(perf.FanRPM, maxFanRPM); maxFanRPM > 0 {
			s.FanPct = ptr(pct)
		}
		if perf.Power > 0 {
			s.PowerW = ptr(d3dkmtPowerW(perf.Power))
		}
	}

	var usedMB, totalMB int
	for seg := uint32(0); seg < nbSegments; seg++ {
		limit, resident, err := d3dkmQuerySegmentStats(luid, seg)
		if err != nil {
			continue
		}
		usedMB += int(resident / bytesPerMB)
		totalMB += int(limit / bytesPerMB)
	}
	if totalMB > 0 {
		s.VramUsedMB, s.VramTotalMB = ptr(usedMB), ptr(totalMB)
	}

	if util, ok := pdhUtil[luid]; ok {
		s.UtilPct = ptr(util)
	} else if util, ok := p.nodeUtil(luid, nodeCount); ok {
		s.UtilPct = ptr(util)
	}
	return s, true
}

// nodeUtil computes utilization of the busiest node from running time
// deltas since the previous read. The first read only seeds the counters.
func (p *d3dkmt) nodeUtil(luid LUID, nodeCount uint32) (float64, bool) {
	now := time.Now()
	prev := p.prev[luid]
	elapsed100ns := now.Sub(p.prevAt[luid]).Nanoseconds() / 100
	cur := make(map[uint32]nodeRunningTimes, nodeCount)

	var util float64
	seen := false
	for node := uint32(0); node < nodeCount; node++ {
		globalRT, systemRT, err := d3dkmQueryNodeStats(luid, node)
		if err != nil {
			continue
		}
		rt := nodeRunningTimes{Global: globalRT, System: systemRT}
		cur[node] = rt
		if before, ok := prev[node]; ok {
			if u := d3dkmtNodeUtil(before, rt, elapsed100ns); u >= 0 {
				util = max(util, u)
				seen = true
			}
		}
	}
	p.prev[luid], p.prevAt[luid] = cur, now
	return util, seen
}
