package core

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	"golang.org/x/sys/windows"
)

// lockName is shared by every voice process of the user, whatever MCP
// protocol version or client started it, so only one utterance plays at once.
const lockName = `Local\voice-playback`

// HoldPlayback keeps every voice process silent until release is called;
// listening uses it so no agent talks into the microphone.
func HoldPlayback(ctx context.Context) (release func(), err error) { return acquirePlayback(ctx) }

// acquirePlayback waits for the cross-process playback mutex. A mutex (not a
// semaphore) is used so a crashed holder releases it as abandoned. Mutexes
// are owned by a thread, so the hold happens on one locked OS thread.
func acquirePlayback(ctx context.Context) (release func(), err error) {
	acquired := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		name, _ := windows.UTF16PtrFromString(lockName)
		h, err := windows.CreateMutex(nil, false, name)
		if h == 0 {
			acquired <- fmt.Errorf("playback lock: %w", err)
			return
		}
		defer windows.CloseHandle(h)
		for {
			ev, err := windows.WaitForSingleObject(h, 100)
			switch ev {
			case windows.WAIT_OBJECT_0, windows.WAIT_ABANDONED:
				acquired <- nil
				<-done
				windows.ReleaseMutex(h)
				return
			case uint32(windows.WAIT_TIMEOUT):
				if ctx.Err() != nil {
					acquired <- ctx.Err()
					return
				}
			default:
				acquired <- fmt.Errorf("playback lock: %w", err)
				return
			}
		}
	}()
	if err := <-acquired; err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }, nil
}
