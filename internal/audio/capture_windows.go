package audio

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procInGetNumDevs      = winmm.NewProc("waveInGetNumDevs")
	procInGetDevCaps      = winmm.NewProc("waveInGetDevCapsW")
	procInOpen            = winmm.NewProc("waveInOpen")
	procInPrepareHeader   = winmm.NewProc("waveInPrepareHeader")
	procInUnprepareHeader = winmm.NewProc("waveInUnprepareHeader")
	procInAddBuffer       = winmm.NewProc("waveInAddBuffer")
	procInStart           = winmm.NewProc("waveInStart")
	procInReset           = winmm.NewProc("waveInReset")
	procInClose           = winmm.NewProc("waveInClose")
)

type waveInCaps struct {
	Mid, Pid      uint16
	DriverVersion uint32
	Pname         [32]uint16
	Formats       uint32
	Channels      uint16
	Reserved1     uint16
}

// InputDevices lists microphones in winmm order (names truncated to 31
// characters by winmm).
func InputDevices() ([]string, error) {
	if err := procInGetNumDevs.Find(); err != nil {
		return nil, err
	}
	n, _, _ := procInGetNumDevs.Call()
	out := make([]string, 0, n)
	for i := uintptr(0); i < n; i++ {
		var caps waveInCaps
		if rc, _, _ := procInGetDevCaps.Call(i, uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps)); rc != 0 {
			out = append(out, fmt.Sprintf("<device %d unavailable>", i))
			continue
		}
		out = append(out, windows.UTF16ToString(caps.Pname[:]))
	}
	return out, nil
}

// Capture records mono 16-bit audio into a ring of buffers.
type Capture struct {
	h    windows.Handle
	rate int
	hdrs []*waveHdr
	bufs [][]int16
	next int
}

const captureBuffers = 8

// OpenCapture starts recording from the named microphone (substring match;
// empty means the system default) in chunks of chunk duration.
func OpenCapture(name string, rate int, chunk time.Duration) (*Capture, error) {
	id := uintptr(waveMapper)
	if name != "" {
		names, err := InputDevices()
		if err != nil {
			return nil, err
		}
		found := -1
		for i, n := range names {
			if strings.Contains(strings.ToLower(n), strings.ToLower(name)) {
				found = i
				break
			}
		}
		if found < 0 {
			return nil, fmt.Errorf("microphone %q not found; available: %s", name, strings.Join(names, ", "))
		}
		id = uintptr(found)
	}
	wf := waveFormatEx{FormatTag: formatPCM, Channels: 1, SamplesPerSec: uint32(rate), AvgBytesPerSec: uint32(rate * 2), BlockAlign: 2, BitsPerSample: 16}
	c := &Capture{rate: rate}
	if rc, _, _ := procInOpen.Call(uintptr(unsafe.Pointer(&c.h)), id, uintptr(unsafe.Pointer(&wf)), 0, 0, callbackNull); rc != 0 {
		return nil, mmError("open microphone", rc)
	}
	samples := int(float64(rate) * chunk.Seconds())
	for i := 0; i < captureBuffers; i++ {
		buf := make([]int16, samples)
		hdr := &waveHdr{Data: uintptr(unsafe.Pointer(&buf[0])), BufferLength: uint32(2 * samples)}
		c.bufs, c.hdrs = append(c.bufs, buf), append(c.hdrs, hdr)
		if rc, _, _ := procInPrepareHeader.Call(uintptr(c.h), uintptr(unsafe.Pointer(hdr)), unsafe.Sizeof(*hdr)); rc != 0 {
			c.Close()
			return nil, mmError("prepare microphone buffer", rc)
		}
		if rc, _, _ := procInAddBuffer.Call(uintptr(c.h), uintptr(unsafe.Pointer(hdr)), unsafe.Sizeof(*hdr)); rc != 0 {
			c.Close()
			return nil, mmError("queue microphone buffer", rc)
		}
	}
	if rc, _, _ := procInStart.Call(uintptr(c.h)); rc != 0 {
		c.Close()
		return nil, mmError("start microphone", rc)
	}
	return c, nil
}

// Read returns the next recorded chunk as float samples.
func (c *Capture) Read(ctx context.Context) ([]float32, error) {
	hdr, buf := c.hdrs[c.next], c.bufs[c.next]
	for atomic.LoadUint32(&hdr.Flags)&whdrDone == 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	n := int(atomic.LoadUint32(&hdr.BytesRecorded)) / 2
	out := make([]float32, n)
	for i, s := range buf[:n] {
		out[i] = float32(s) / 32768
	}
	// Hand the buffer back to the driver.
	atomic.StoreUint32(&hdr.Flags, atomic.LoadUint32(&hdr.Flags)&^whdrDone)
	procInAddBuffer.Call(uintptr(c.h), uintptr(unsafe.Pointer(hdr)), unsafe.Sizeof(*hdr))
	c.next = (c.next + 1) % len(c.hdrs)
	return out, nil
}

func (c *Capture) Close() {
	if c.h == 0 {
		return
	}
	procInReset.Call(uintptr(c.h))
	for _, hdr := range c.hdrs {
		procInUnprepareHeader.Call(uintptr(c.h), uintptr(unsafe.Pointer(hdr)), unsafe.Sizeof(*hdr))
	}
	procInClose.Call(uintptr(c.h))
	c.h = 0
	runtime.KeepAlive(c.bufs)
}
