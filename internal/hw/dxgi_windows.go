//go:build windows && amd64

package hw

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	dxgiDLL               = windows.NewLazySystemDLL("dxgi.dll")
	procCreateDXGIFactory = dxgiDLL.NewProc("CreateDXGIFactory")
)

var iidIDXGIFactory = windows.GUID{
	Data1: 0x7b7166ec,
	Data2: 0x21c7,
	Data3: 0x44ae,
	Data4: [8]byte{0xb2, 0x1a, 0xc9, 0xae, 0x32, 0x1a, 0xe3, 0x69},
}

const dxgiErrorNotFound = 0x887a0002

type dxgiObject struct {
	vtable *[10]uintptr
}

type dxgiAdapterDesc struct {
	Description           [128]uint16
	VendorID              uint32
	DeviceID              uint32
	SubSysID              uint32
	Revision              uint32
	DedicatedVideoMemory  uintptr
	DedicatedSystemMemory uintptr
	SharedSystemMemory    uintptr
	AdapterLUIDLowPart    uint32
	AdapterLUIDHighPart   int32
}

func init() {
	if unsafe.Sizeof(dxgiAdapterDesc{}) != 304 {
		panic("unexpected DXGI_ADAPTER_DESC size")
	}
}

func detectDXGI() ([]detectedAccelerator, error) {
	if err := procCreateDXGIFactory.Find(); err != nil {
		return nil, err
	}
	var factory *dxgiObject
	hresult, _, _ := procCreateDXGIFactory.Call(
		uintptr(unsafe.Pointer(&iidIDXGIFactory)),
		uintptr(unsafe.Pointer(&factory)),
	)
	if hresultFailed(hresult) || factory == nil {
		return nil, fmt.Errorf("CreateDXGIFactory failed: HRESULT 0x%08x", uint32(hresult))
	}
	defer releaseDXGI(factory)

	var result []detectedAccelerator
	for index := uint32(0); ; index++ {
		var adapter *dxgiObject
		hresult, _, _ = syscall.SyscallN(
			factory.vtable[7],
			uintptr(unsafe.Pointer(factory)),
			uintptr(index),
			uintptr(unsafe.Pointer(&adapter)),
		)
		if uint32(hresult) == dxgiErrorNotFound {
			break
		}
		if hresultFailed(hresult) || adapter == nil {
			break
		}

		var desc dxgiAdapterDesc
		descResult, _, _ := syscall.SyscallN(
			adapter.vtable[8],
			uintptr(unsafe.Pointer(adapter)),
			uintptr(unsafe.Pointer(&desc)),
		)
		releaseDXGI(adapter)
		if hresultFailed(descResult) {
			continue
		}

		model := windows.UTF16ToString(desc.Description[:])
		if isSoftwareDisplayAdapter(model) {
			continue
		}
		vendor := windowsPCIVendor(desc.VendorID)
		memory := AcceleratorMemory{Kind: "unknown"}
		if desc.DedicatedVideoMemory > 0 {
			capacity := uint64(desc.DedicatedVideoMemory)
			memory = AcceleratorMemory{Kind: "dedicated", CapacityBytes: &capacity}
		} else if desc.SharedSystemMemory > 0 {
			capacity := uint64(desc.SharedSystemMemory)
			memory = AcceleratorMemory{Kind: "shared_system", CapacityBytes: &capacity}
		}
		result = append(result, detectedAccelerator{
			identity: fmt.Sprintf("luid:%08x:%08x", uint32(desc.AdapterLUIDHighPart), desc.AdapterLUIDLowPart),
			value: Accelerator{
				Kind:   "gpu",
				Vendor: nonEmptyStringPtr(vendor),
				Model:  nonEmptyStringPtr(model),
				Memory: memory,
			},
		})
	}
	return result, nil
}

func releaseDXGI(object *dxgiObject) {
	if object != nil && object.vtable != nil {
		syscall.SyscallN(object.vtable[2], uintptr(unsafe.Pointer(object)))
	}
}

func hresultFailed(result uintptr) bool {
	return int32(uint32(result)) < 0
}

func windowsPCIVendor(id uint32) string {
	switch id {
	case 0x10de:
		return "NVIDIA"
	case 0x1002, 0x1022:
		return "AMD"
	case 0x8086:
		return "Intel"
	case 0x106b:
		return "Apple"
	default:
		return ""
	}
}
