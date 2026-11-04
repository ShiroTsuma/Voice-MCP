package audio

import (
	"errors"
	"math"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// Sessions adjusts Windows audio sessions (the per-app entries of the volume
// mixer) on one COM thread: it ducks other apps while voice speaks and names
// voice's own session.
type Sessions struct {
	jobs chan func()

	mu     sync.Mutex
	ducked map[string]float32 // session instance id -> volume before ducking
}

func NewSessions() (*Sessions, error) {
	s := &Sessions{jobs: make(chan func()), ducked: map[string]float32{}}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
			var oleErr *ole.OleError
			if !errors.As(err, &oleErr) || oleErr.Code() != 1 {
				ready <- err
				return
			}
		}
		ready <- nil
		for job := range s.jobs {
			job()
		}
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Sessions) do(fn func()) {
	done := make(chan struct{})
	s.jobs <- func() { fn(); close(done) }
	<-done
}

type session struct {
	id     string
	pid    uint32
	active bool
	vol    *wca.ISimpleAudioVolume
	ctl    *wca.IAudioSessionControl
}

// each calls fn for every session on every active output device.
func each(fn func(session)) error {
	var en *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &en); err != nil {
		return err
	}
	defer en.Release()
	var devices *wca.IMMDeviceCollection
	if err := en.EnumAudioEndpoints(wca.ERender, wca.DEVICE_STATE_ACTIVE, &devices); err != nil {
		return err
	}
	defer devices.Release()
	var n uint32
	devices.GetCount(&n)
	for i := uint32(0); i < n; i++ {
		var dev *wca.IMMDevice
		if devices.Item(i, &dev) != nil {
			continue
		}
		var mgr *wca.IAudioSessionManager2
		if dev.Activate(wca.IID_IAudioSessionManager2, wca.CLSCTX_ALL, nil, &mgr) == nil {
			var list *wca.IAudioSessionEnumerator
			if mgr.GetSessionEnumerator(&list) == nil {
				var count int
				list.GetCount(&count)
				for j := 0; j < count; j++ {
					var ctl *wca.IAudioSessionControl
					if list.GetSession(j, &ctl) != nil {
						continue
					}
					var ctl2 *wca.IAudioSessionControl2
					var vol *wca.ISimpleAudioVolume
					if ctl.PutQueryInterface(wca.IID_IAudioSessionControl2, &ctl2) == nil &&
						ctl.PutQueryInterface(wca.IID_ISimpleAudioVolume, &vol) == nil {
						var sess session
						ctl2.GetProcessId(&sess.pid)
						ctl2.GetSessionInstanceIdentifier(&sess.id)
						var state uint32
						ctl.GetState(&state)
						sess.active = state == wca.AudioSessionStateActive
						sess.vol, sess.ctl = vol, ctl
						fn(sess)
					}
					if vol != nil {
						vol.Release()
					}
					if ctl2 != nil {
						ctl2.Release()
					}
					ctl.Release()
				}
				list.Release()
			}
			mgr.Release()
		}
		dev.Release()
	}
	return nil
}

// Duck lowers every other app that is playing to level times its volume.
func (s *Sessions) Duck(level float64) {
	if level >= 1 {
		return
	}
	s.do(func() {
		self := uint32(os.Getpid())
		var targets []session
		s.mu.Lock()
		each(func(sess session) {
			if sess.pid == self || sess.pid == 0 || !sess.active {
				return
			}
			if _, done := s.ducked[sess.id]; done {
				return
			}
			var v float32
			if sess.vol.GetMasterVolume(&v) != nil {
				return
			}
			s.ducked[sess.id] = v
			sess.vol.AddRef()
			targets = append(targets, sess)
		})
		s.mu.Unlock()
		fade(targets, func(sess session, t float64) float32 {
			orig := s.ducked[sess.id]
			return orig * float32(1-t*(1-level))
		})
		for _, sess := range targets {
			sess.vol.Release()
		}
	})
}

// Restore brings ducked apps back, unless the user changed their volume
// in the meantime.
func (s *Sessions) Restore(level float64) {
	s.do(func() {
		s.mu.Lock()
		saved := s.ducked
		s.ducked = map[string]float32{}
		s.mu.Unlock()
		if len(saved) == 0 {
			return
		}
		var targets []session
		each(func(sess session) {
			orig, ok := saved[sess.id]
			if !ok {
				return
			}
			var v float32
			if sess.vol.GetMasterVolume(&v) != nil || math.Abs(float64(v)-float64(orig)*level) > 0.02 {
				return
			}
			sess.vol.AddRef()
			targets = append(targets, sess)
		})
		fade(targets, func(sess session, t float64) float32 {
			orig := saved[sess.id]
			return orig * float32(level+t*(1-level))
		})
		for _, sess := range targets {
			sess.vol.Release()
		}
	})
}

func fade(targets []session, at func(session, float64) float32) {
	if len(targets) == 0 {
		return
	}
	const steps = 6
	for i := 1; i <= steps; i++ {
		for _, sess := range targets {
			sess.vol.SetMasterVolume(at(sess, float64(i)/steps), nil)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func setString(this, method uintptr, value string) {
	p, err := syscall.UTF16PtrFromString(value)
	if err != nil {
		return
	}
	syscall.SyscallN(method, this, uintptr(unsafe.Pointer(p)), 0)
	runtime.KeepAlive(p)
}

type SessionInfo struct {
	PID    uint32
	Active bool
	Volume float32
	Name   string
}

// List reports the sessions on all active output devices.
func (s *Sessions) List() []SessionInfo {
	var out []SessionInfo
	s.do(func() {
		each(func(sess session) {
			info := SessionInfo{PID: sess.pid, Active: sess.active}
			sess.vol.GetMasterVolume(&info.Volume)
			sess.ctl.GetDisplayName(&info.Name)
			out = append(out, info)
		})
	})
	return out
}

// NameSelf gives this process's sessions a display name and icon in the
// volume mixer.
func (s *Sessions) NameSelf(name, icon string) {
	s.do(func() {
		self := uint32(os.Getpid())
		each(func(sess session) {
			if sess.pid != self {
				return
			}
			// go-wca passes Go string headers where COM wants LPWSTR, so
			// call the vtable with UTF-16 strings directly.
			setString(uintptr(unsafe.Pointer(sess.ctl)), sess.ctl.VTable().SetDisplayName, name)
			if icon != "" {
				setString(uintptr(unsafe.Pointer(sess.ctl)), sess.ctl.VTable().SetIconPath, icon)
			}
		})
	})
}
