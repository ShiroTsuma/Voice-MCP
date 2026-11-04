package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"voice/internal/audio"
	"voice/internal/core"
	"voice/internal/hotkey"
	"voice/internal/listen"
	"voice/internal/overlay"
	"voice/internal/preset"
	"voice/internal/service"
)

const (
	hotkeyStop = iota + 1
	hotkeyMute
	hotkeyDictate
	hotkeyCancel // Esc, only while dictating
)

// controller runs the background process's live features: ducking, hotkeys,
// barge-in, dictation and freeing idle models. apply re-reads cfg.Serve, so
// the tray and the settings window change them without a restart.
type controller struct {
	cfg    *preset.Config
	sp     *core.Speaker
	l      *listen.Listener
	agents *service.Agents
	ov     *overlay.Overlay // dictation's state in the screen corner; nil if it could not open
	// onSpeaking learns when speech starts and ends, onDictating when the user
	// starts and stops dictating (the speaking popup).
	onSpeaking, onDictating func(on bool)
	sessions                *audio.Sessions
	keys                    hotkey.Set

	mu        sync.Mutex
	duck      float64
	bargeIn   bool
	restore   *time.Timer
	stopBarge context.CancelFunc
	keyErrs   []string
	dictating chan struct{} // closed by the second dictation press
	dict      dictation     // what the settings window shows meanwhile
	// dictCancel is set when the user cancels the dictation in progress (Esc).
	dictCancel *atomic.Bool
	theme      string // the overlay theme applied last
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s "+format+"\n", append([]any{time.Now().Format(time.TimeOnly)}, args...)...)
}

func startExtras(ctx context.Context, cfg *preset.Config, sp *core.Speaker, l *listen.Listener, agents *service.Agents) *controller {
	c := &controller{cfg: cfg, sp: sp, l: l, agents: agents}
	overlay.Logf = logf
	if ov, err := overlay.Start(); err != nil {
		logf("%v", err)
	} else {
		c.ov = ov
	}
	agents.OnChange = c.queueChanged
	if s, err := audio.NewSessions(); err != nil {
		logf("audio sessions: %v", err)
	} else {
		c.sessions = s
		exe, _ := os.Executable()
		s.NameSelf("voice", exe+",0")
	}
	c.apply()
	sp.OnActivity(c.onActivity)
	go func() {
		<-ctx.Done()
		if c.sessions != nil {
			c.sessions.Restore(c.duckLevel())
		}
	}()
	go c.janitor(ctx)
	return c
}

func (c *controller) duckLevel() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.duck
}

// apply takes the current [serve] settings.
func (c *controller) apply() {
	sv := c.cfg.Serve
	c.mu.Lock()
	c.duck = 0.35
	if sv.Duck != nil {
		c.duck = max(0, min(1, *sv.Duck))
	}
	c.bargeIn = sv.BargeIn != nil && *sv.BargeIn
	c.keyErrs = nil
	c.mu.Unlock()
	c.applyTheme()
	for _, k := range []struct {
		id   int
		spec string
		fn   func()
	}{
		{hotkeyStop, sv.StopHotkey, c.sp.Stop},
		{hotkeyMute, sv.MuteHotkey, func() { c.sp.SetMuted(!c.sp.Status().Muted) }},
		{hotkeyDictate, sv.DictateHotkey, c.dictate},
	} {
		if err := c.keys.Register(k.id, k.spec, k.fn); err != nil {
			logf("%v", err)
			c.mu.Lock()
			c.keyErrs = append(c.keyErrs, err.Error())
			c.mu.Unlock()
		}
	}
}

func (c *controller) hotkeyErrors() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.keyErrs...)
}

func (c *controller) onActivity(speaking bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.restore != nil {
		c.restore.Stop()
		c.restore = nil
	}
	duck := c.duck
	if fn := c.onSpeaking; fn != nil {
		go fn(speaking)
	}
	if speaking {
		if c.sessions != nil && duck < 1 {
			go c.sessions.Duck(duck)
		}
		if c.bargeIn && c.stopBarge == nil && c.l.Available() {
			ctx, cancel := context.WithCancel(context.Background())
			c.stopBarge = cancel
			go func() {
				// Ignore the first moment: the chime and the voice's own
				// onset reach the microphone through speakers.
				select {
				case <-ctx.Done():
					return
				case <-time.After(400 * time.Millisecond):
				}
				if talked, err := c.l.WaitForSpeech(ctx, 300*time.Millisecond); err != nil {
					logf("barge-in: %v", err)
				} else if talked {
					c.sp.Stop()
				}
			}()
		}
		return
	}
	if c.stopBarge != nil {
		c.stopBarge()
		c.stopBarge = nil
	}
	if c.sessions != nil && duck < 1 {
		// A moment later, so back-to-back sentences do not pump other apps.
		c.restore = time.AfterFunc(600*time.Millisecond, func() { c.sessions.Restore(duck) })
	}
}

func (c *controller) janitor(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		after := unloadAfter(c.cfg)
		if after <= 0 {
			continue
		}
		for _, name := range c.sp.UnloadIdle(after) {
			logf("unloaded idle engine %s", name)
		}
		if c.l.UnloadIdle(after) {
			logf("unloaded idle recognizer")
		}
	}
}

func unloadAfter(cfg *preset.Config) time.Duration {
	v := cfg.Serve.UnloadAfter
	if v == "" {
		return 15 * time.Minute
	}
	if v == "0" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		logf("serve.unload_after %q: %v; using 15m", v, err)
		return 15 * time.Minute
	}
	return d
}
