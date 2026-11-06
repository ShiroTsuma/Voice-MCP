package service

import (
	"context"
	"time"
)

// A session that registers with keep_warm has its voice kept loaded while it
// runs: a GPU voice's server unloads after the idle time (serve.unload_after)
// and takes many seconds to load again, which a role-play's next line should
// not wait for. Opt-in, because a loaded GPU voice holds graphics memory that
// other programs and voices could use.

// keepWarmEvery is how often the voices of keep-warm sessions are touched;
// well under the shortest idle time anyone sets.
var keepWarmEvery = time.Minute

// setKeepWarm records register's keep_warm for pid and, when on, loads the
// session's voice. It says what that does on this PC, or "" when off.
func (s *Service) setKeepWarm(pid uint32, on bool) string {
	s.Agents.SetKeepWarm(pid, on)
	if !on {
		return ""
	}
	if s.Speaker == nil {
		return "keep_warm: voice does not speak here, so there is no voice to keep loaded."
	}
	name, gpu := s.Speaker.KeepWarm(s.sessionPreset(pid), true)
	if !gpu {
		s.Agents.SetKeepWarm(pid, false)
		return "keep_warm: this session's voice (" + name + ") does not run in a server of its own, so it loads quickly and there is nothing to keep loaded."
	}
	return "keep_warm: voice keeps this session's voice (" + name + ") loaded while the session runs."
}

// sessionPreset is the preset pid speaks with when it names none, as Speak
// picks it: its character's, else its client's, else "" (the active one).
func (s *Service) sessionPreset(pid uint32) string {
	client := ""
	s.Agents.mu.Lock()
	if st := s.Agents.seen[pid]; st != nil {
		client = st.client
	}
	s.Agents.mu.Unlock()
	return s.presetFor(s.characterPreset(pid), client)
}

// KeepWarmLoop touches the voices of keep-warm sessions until ctx ends. A
// voice that is not loaded (freed for another within the GPU memory budget,
// or after a restart) stays down until the session speaks.
func (s *Service) KeepWarmLoop(ctx context.Context) {
	t := time.NewTicker(keepWarmEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.keepWarmTick()
	}
}

func (s *Service) keepWarmTick() {
	if s.Speaker == nil {
		return
	}
	done := map[string]bool{}
	for _, pid := range s.Agents.KeepWarm() {
		preset := s.sessionPreset(pid)
		if done[preset] {
			continue
		}
		done[preset] = true
		s.Speaker.KeepWarm(preset, false)
	}
}
