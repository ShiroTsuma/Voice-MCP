//go:build windows

package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// adapterMemory is the GPU voices' card's free and total memory from Windows
// itself, so it works the same for NVIDIA, AMD and Intel:
//
//   - DXGI lists the adapters with their LUID and own memory
//     (DedicatedVideoMemory). The registry's HardwareInformation.qwMemorySize
//     (gpuTotalGB) has the size too, but nothing there names the LUID, so with
//     two cards it could not be paired with a counter.
//   - The "GPU Adapter Memory" performance counters say how much of each
//     adapter's own memory is in use, by every process, WSL's included. Their
//     instances are named after the LUID ("luid_0x00000000_0x0000D1A3_phys_0"),
//     which pairs them with DXGI's list. They are added by their English name,
//     so a Polish or any other Windows finds them.
//
// It is read afresh each time: a driver update or reset gives the card a new
// LUID.
func adapterMemory() (free, total float64, known bool) {
	adapters, ok := dxgiAdapters()
	if !ok {
		return 0, 0, false
	}
	usage, ok := dedicatedUsage()
	if !ok {
		return 0, 0, false
	}
	return pickAdapterMemory(adapters, usage)
}

var (
	dxgi                   = windows.NewLazySystemDLL("dxgi.dll")
	procCreateDXGIFactory1 = dxgi.NewProc("CreateDXGIFactory1")

	pdh                              = windows.NewLazySystemDLL("pdh.dll")
	procPdhOpenQueryW                = pdh.NewProc("PdhOpenQueryW")
	procPdhAddEnglishCounterW        = pdh.NewProc("PdhAddEnglishCounterW")
	procPdhCollectQueryData          = pdh.NewProc("PdhCollectQueryData")
	procPdhGetFormattedCounterArrayW = pdh.NewProc("PdhGetFormattedCounterArrayW")
	procPdhCloseQuery                = pdh.NewProc("PdhCloseQuery")
)

// IID_IDXGIFactory1, {770aae78-f26f-4dba-a829-253c83d1b387}.
var iidDXGIFactory1 = windows.GUID{Data1: 0x770aae78, Data2: 0xf26f, Data3: 0x4dba, Data4: [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87}}

// Vtable slots: IUnknown (0-2), IDXGIObject (3-6), IDXGIFactory (7-11),
// IDXGIFactory1 (12-13); IDXGIAdapter (7-9), IDXGIAdapter1 (10).
const (
	vtRelease       = 2
	vtEnumAdapters1 = 12
	vtGetDesc1      = 10

	dxgiErrorNotFound       = 0x887A0002
	dxgiAdapterFlagSoftware = 2
)

// dxgiAdapterDesc1 is DXGI_ADAPTER_DESC1.
type dxgiAdapterDesc1 struct {
	Description           [128]uint16
	VendorID              uint32
	DeviceID              uint32
	SubSysID              uint32
	Revision              uint32
	DedicatedVideoMemory  uintptr
	DedicatedSystemMemory uintptr
	SharedSystemMemory    uintptr
	LuidLow               uint32
	LuidHigh              int32
	Flags                 uint32
}

// comObject is a COM interface pointer's target: its first word is the
// vtable.
type comObject struct{ vtbl *[16]uintptr }

// comCall calls the COM method in slot i of obj's vtable.
func comCall(obj *comObject, i int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(obj.vtbl[i], append([]uintptr{uintptr(unsafe.Pointer(obj))}, args...)...)
	return r
}

// dxgiAdapters lists the graphics adapters through DXGI.
func dxgiAdapters() ([]gpuAdapter, bool) {
	if procCreateDXGIFactory1.Find() != nil {
		return nil, false
	}
	var factory *comObject
	if r, _, _ := procCreateDXGIFactory1.Call(uintptr(unsafe.Pointer(&iidDXGIFactory1)), uintptr(unsafe.Pointer(&factory))); r != 0 || factory == nil {
		return nil, false
	}
	defer comCall(factory, vtRelease)
	var list []gpuAdapter
	for i := uintptr(0); i < 16; i++ {
		var adapter *comObject
		r := comCall(factory, vtEnumAdapters1, i, uintptr(unsafe.Pointer(&adapter)))
		if uint32(r) == dxgiErrorNotFound || r != 0 || adapter == nil {
			break
		}
		var d dxgiAdapterDesc1
		ok := comCall(adapter, vtGetDesc1, uintptr(unsafe.Pointer(&d))) == 0
		comCall(adapter, vtRelease)
		if ok {
			list = append(list, gpuAdapter{
				luidHigh:  d.LuidHigh,
				luidLow:   d.LuidLow,
				dedicated: uint64(d.DedicatedVideoMemory),
				software:  d.Flags&dxgiAdapterFlagSoftware != 0,
			})
		}
	}
	return list, len(list) > 0
}

const (
	pdhFmtLarge = 0x00000400
	pdhMoreData = 0x800007D2
)

// pdhFmtCounterValueItem is PDH_FMT_COUNTERVALUE_ITEM_W with a LONGLONG value.
type pdhFmtCounterValueItem struct {
	Name    *uint16
	CStatus uint32
	_       uint32
	Value   int64
}

// dedicatedUsage reads \GPU Adapter Memory(*)\Dedicated Usage: bytes of each
// adapter's own memory in use, by counter instance name.
func dedicatedUsage() (map[string]float64, bool) {
	if procPdhAddEnglishCounterW.Find() != nil {
		return nil, false
	}
	var query, counter uintptr
	if r, _, _ := procPdhOpenQueryW.Call(0, 0, uintptr(unsafe.Pointer(&query))); r != 0 {
		return nil, false
	}
	defer procPdhCloseQuery.Call(query)
	path, _ := windows.UTF16PtrFromString(`\GPU Adapter Memory(*)\Dedicated Usage`)
	if r, _, _ := procPdhAddEnglishCounterW.Call(query, uintptr(unsafe.Pointer(path)), 0, uintptr(unsafe.Pointer(&counter))); r != 0 {
		return nil, false
	}
	// A raw count: one sample is a value.
	if r, _, _ := procPdhCollectQueryData.Call(query); r != 0 {
		return nil, false
	}
	// The first call says the size; instances may come and go in between.
	for try := 0; try < 3; try++ {
		var size, count uint32
		r, _, _ := procPdhGetFormattedCounterArrayW.Call(counter, pdhFmtLarge, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
		if uint32(r) != pdhMoreData || size == 0 {
			return nil, false
		}
		buf := make([]uint64, size/8+1) // 8-byte aligned; names follow the items
		r, _, _ = procPdhGetFormattedCounterArrayW.Call(counter, pdhFmtLarge, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buf[0])))
		if uint32(r) == pdhMoreData {
			continue
		}
		if r != 0 {
			return nil, false
		}
		items := unsafe.Slice((*pdhFmtCounterValueItem)(unsafe.Pointer(&buf[0])), count)
		usage := make(map[string]float64, count)
		for _, it := range items {
			if it.CStatus > 1 { // PDH_CSTATUS_VALID_DATA, PDH_CSTATUS_NEW_DATA
				continue
			}
			usage[windows.UTF16PtrToString(it.Name)] = float64(it.Value)
		}
		return usage, len(usage) > 0
	}
	return nil, false
}
