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

// winmm waveOut is used instead of WASAPI because it addresses any output,
// including virtual cables, by index with a few calls and no COM.

var (
	winmm               = windows.NewLazySystemDLL("winmm.dll")
	procGetNumDevs      = winmm.NewProc("waveOutGetNumDevs")
	procGetDevCaps      = winmm.NewProc("waveOutGetDevCapsW")
	procOpen            = winmm.NewProc("waveOutOpen")
	procPrepareHeader   = winmm.NewProc("waveOutPrepareHeader")
	procWrite           = winmm.NewProc("waveOutWrite")
	procUnprepareHeader = winmm.NewProc("waveOutUnprepareHeader")
	procReset           = winmm.NewProc("waveOutReset")
	procClose           = winmm.NewProc("waveOutClose")
	procGetErrorText    = winmm.NewProc("waveOutGetErrorTextW")
)

const (
	waveMapper   = 0xFFFFFFFF
	callbackNull = 0
	whdrDone     = 1
	formatPCM    = 1
)

type waveOutCaps struct {
	Mid, Pid      uint16
	DriverVersion uint32
	Pname         [32]uint16
	Formats       uint32
	Channels      uint16
	Reserved1     uint16
	Support       uint32
}

type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
}

type waveHdr struct {
	Data          uintptr
	BufferLength  uint32
	BytesRecorded uint32
	User          uintptr
	Flags         uint32
	Loops         uint32
	Next          uintptr
	Reserved      uintptr
}

func mmError(op string, rc uintptr) error {
	buf := make([]uint16, 256)
	procGetErrorText.Call(rc, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return fmt.Errorf("%s: %s (MMSYSERR %d)", op, windows.UTF16ToString(buf), rc)
}

// Devices lists output device names in winmm order. winmm truncates names to
// 31 characters.
func Devices() ([]string, error) {
	if err := procGetNumDevs.Find(); err != nil {
		return nil, err
	}
	n, _, _ := procGetNumDevs.Call()
	out := make([]string, 0, n)
	for i := uintptr(0); i < n; i++ {
		var caps waveOutCaps
		if rc, _, _ := procGetDevCaps.Call(i, uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps)); rc != 0 {
			out = append(out, fmt.Sprintf("<device %d unavailable>", i))
			continue
		}
		out = append(out, windows.UTF16ToString(caps.Pname[:]))
	}
	return out, nil
}

type device struct {
	h windows.Handle
}

func openDevice(name string, rate int) (*device, error) {
	id := uintptr(waveMapper)
	if name != "" {
		names, err := Devices()
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
			return nil, fmt.Errorf("audio device %q not found; available: %s", name, strings.Join(names, ", "))
		}
		id = uintptr(found)
	}
	wf := waveFormatEx{
		FormatTag:      formatPCM,
		Channels:       1,
		SamplesPerSec:  uint32(rate),
		AvgBytesPerSec: uint32(rate * 2),
		BlockAlign:     2,
		BitsPerSample:  16,
	}
	d := &device{}
	if rc, _, _ := procOpen.Call(uintptr(unsafe.Pointer(&d.h)), id, uintptr(unsafe.Pointer(&wf)), 0, 0, callbackNull); rc != 0 {
		return nil, mmError("open audio device", rc)
	}
	return d, nil
}

func (d *device) play(ctx context.Context, pcm []float32) error {
	buf := make([]int16, len(pcm))
	for i, s := range pcm {
		buf[i] = int16(max(-1, min(1, s)) * 32767)
	}
	hdr := &waveHdr{Data: uintptr(unsafe.Pointer(&buf[0])), BufferLength: uint32(2 * len(buf))}
	size := unsafe.Sizeof(*hdr)
	if rc, _, _ := procPrepareHeader.Call(uintptr(d.h), uintptr(unsafe.Pointer(hdr)), size); rc != 0 {
		return mmError("prepare audio", rc)
	}
	defer func() {
		procUnprepareHeader.Call(uintptr(d.h), uintptr(unsafe.Pointer(hdr)), size)
		// The driver reads buf until the header is unprepared.
		runtime.KeepAlive(buf)
	}()
	if rc, _, _ := procWrite.Call(uintptr(d.h), uintptr(unsafe.Pointer(hdr)), size); rc != 0 {
		return mmError("play audio", rc)
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	// The driver sets WHDR_DONE from its own thread.
	for atomic.LoadUint32(&hdr.Flags)&whdrDone == 0 {
		select {
		case <-ctx.Done():
			// Reset returns all pending buffers marked done.
			procReset.Call(uintptr(d.h))
			return ctx.Err()
		case <-tick.C:
		}
	}
	return nil
}

func (d *device) close() error {
	procReset.Call(uintptr(d.h))
	if rc, _, _ := procClose.Call(uintptr(d.h)); rc != 0 {
		return mmError("close audio device", rc)
	}
	return nil
}
