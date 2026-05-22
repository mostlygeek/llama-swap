//go:build windows

package perf

import (
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	pdhDLL                          = windows.NewLazySystemDLL("pdh.dll")
	procPdhOpenQuery                = pdhDLL.NewProc("PdhOpenQueryW")
	procPdhAddEnglishCounter        = pdhDLL.NewProc("PdhAddEnglishCounterW")
	procPdhCollectQueryData         = pdhDLL.NewProc("PdhCollectQueryData")
	procPdhGetFormattedCounterArray = pdhDLL.NewProc("PdhGetFormattedCounterArrayW")
	procPdhCloseQuery               = pdhDLL.NewProc("PdhCloseQuery")
)

const (
	pdhFmtDouble = 0x00000200
	pdhMoreData  = 0x800007D2
	pdhNoData    = 0x800007D5
)

type pdhCounterValue struct {
	CStatus uint32
	DblVal  float64
}

type pdhCounterValueItem struct {
	SzName   *uint16
	FmtValue pdhCounterValue
}

func init() {
	var item pdhCounterValueItem
	if unsafe.Sizeof(item) != 24 {
		panic(fmt.Sprintf("pdhCounterValueItem size %d != expected 24 on x64", unsafe.Sizeof(item)))
	}
}

type pdhGpuUtil struct {
	query   uintptr
	counter uintptr
}

func initPdhGpuUtil() (*pdhGpuUtil, error) {
	var query uintptr
	if ret, _, _ := procPdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&query))); ret != 0 {
		return nil, fmt.Errorf("PdhOpenQuery: 0x%x", ret)
	}

	path, _ := windows.UTF16PtrFromString(`\GPU Engine(*)\Utilization Percentage`)
	var counter uintptr
	if ret, _, _ := procPdhAddEnglishCounter.Call(
		query, uintptr(unsafe.Pointer(path)), 0, uintptr(unsafe.Pointer(&counter)),
	); ret != 0 {
		procPdhCloseQuery.Call(query)
		return nil, fmt.Errorf("PdhAddEnglishCounter(GPU Engine): 0x%x", ret)
	}

	procPdhCollectQueryData.Call(query)

	return &pdhGpuUtil{query: query, counter: counter}, nil
}

func (p *pdhGpuUtil) close() {
	if p.query != 0 {
		procPdhCloseQuery.Call(p.query)
		p.query = 0
	}
}

func (p *pdhGpuUtil) collect() map[LUID]float64 {
	ret, _, _ := procPdhCollectQueryData.Call(p.query)
	if ret != 0 && ret != pdhNoData {
		return nil
	}

	var bufSize uint32
	var itemCount uint32
	ret, _, _ = procPdhGetFormattedCounterArray.Call(
		p.counter, pdhFmtDouble,
		uintptr(unsafe.Pointer(&bufSize)),
		uintptr(unsafe.Pointer(&itemCount)),
		0,
	)
	if ret != pdhMoreData || itemCount == 0 {
		return nil
	}

	buf := make([]byte, bufSize)
	ret, _, _ = procPdhGetFormattedCounterArray.Call(
		p.counter, pdhFmtDouble,
		uintptr(unsafe.Pointer(&bufSize)),
		uintptr(unsafe.Pointer(&itemCount)),
		uintptr(unsafe.Pointer(&buf[0])),
	)
	if ret != 0 {
		return nil
	}

	itemSize := uint32(unsafe.Sizeof(pdhCounterValueItem{}))
	result := make(map[LUID]float64)

	for i := uint32(0); i < itemCount; i++ {
		item := (*pdhCounterValueItem)(unsafe.Pointer(&buf[i*itemSize]))
		if item.FmtValue.CStatus != 0 {
			continue
		}
		luid, ok := parsePdhLuid(windows.UTF16PtrToString(item.SzName))
		if !ok {
			continue
		}
		result[luid] += item.FmtValue.DblVal
	}

	for luid := range result {
		if result[luid] > 100.0 {
			result[luid] = 100.0
		}
	}

	return result
}

func parsePdhLuid(name string) (LUID, bool) {
	idx := strings.Index(name, "luid_0x")
	if idx < 0 {
		return LUID{}, false
	}
	rest := name[idx+7:]
	parts := strings.SplitN(rest, "_", 4)
	if len(parts) < 3 {
		return LUID{}, false
	}
	hp, err := strconv.ParseUint(parts[0], 16, 32)
	if err != nil {
		return LUID{}, false
	}
	lpStr := strings.TrimPrefix(parts[1], "0x")
	lp, err := strconv.ParseUint(lpStr, 16, 32)
	if err != nil {
		return LUID{}, false
	}
	return LUID{LowPart: uint32(lp), HighPart: int32(hp)}, true
}
