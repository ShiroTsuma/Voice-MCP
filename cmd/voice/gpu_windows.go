//go:build windows

package main

import (
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// gpuTotalGB is the largest graphics card's own memory, in GB, from the
// display adapters' driver keys (NVIDIA, AMD and Intel alike); 0 when unknown.
var gpuTotalGB = sync.OnceValue(func() float64 {
	const class = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, class, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return 0
	}
	defer k.Close()
	names, _ := k.ReadSubKeyNames(-1)
	best := uint64(0)
	for _, n := range names {
		sub, err := registry.OpenKey(k, n, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		if v, _, err := sub.GetIntegerValue("HardwareInformation.qwMemorySize"); err == nil && v > best {
			best = v
		}
		sub.Close()
	}
	return float64(best) / (1 << 30)
})

// gpuKinds are the makers of this PC's graphics cards (nvidia, amd, intel),
// read once from Windows' display adapters: the Integrations window offers a
// GPU voice only where one of its cards can run it.
var gpuKinds = sync.OnceValue(func() map[string]bool {
	kinds := map[string]bool{}
	enum := windows.NewLazySystemDLL("user32.dll").NewProc("EnumDisplayDevicesW")
	type displayDevice struct {
		cb           uint32
		DeviceName   [32]uint16
		DeviceString [128]uint16
		StateFlags   uint32
		DeviceID     [128]uint16
		DeviceKey    [128]uint16
	}
	for i := uint32(0); i < 64; i++ {
		var d displayDevice
		d.cb = uint32(unsafe.Sizeof(d))
		if r, _, _ := enum.Call(0, uintptr(i), uintptr(unsafe.Pointer(&d)), 0); r == 0 {
			break
		}
		if k := gpuKind(windows.UTF16ToString(d.DeviceString[:])); k != "" {
			kinds[k] = true
		}
	}
	return kinds
})

func gpuKind(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "nvidia"):
		return "nvidia"
	case strings.Contains(n, "amd"), strings.Contains(n, "radeon"):
		return "amd"
	case strings.Contains(n, "intel"):
		return "intel"
	}
	return ""
}
