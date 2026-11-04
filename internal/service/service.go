// Package service implements voice's operations once, for every front end:
// the background process runs them, and the MCP server, hooks and CLI reach
// them through the daemon client. Results are JSON-ready values; a
// UserError is a message for the caller, not a failure of voice.
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"voice/internal/audio"
	"voice/internal/clones"
	"voice/internal/core"
	"voice/internal/listen"
	"voice/internal/preset"
	"voice/sidecar"
)

type SpeakArgs struct {
	Text string `json:"text"`
	// Source: "hook" for an announcement voice's hooks make; empty for the agent's own words.
	Source    string `json:"source,omitempty"`
	Intent    string `json:"intent,omitempty"`
	Lang      string `json:"lang,omitempty"`
	Interrupt bool   `json:"interrupt,omitempty"`
	Preset    string `json:"preset,omitempty"`
	Cache     bool   `json:"cache,omitempty"`
	Wait      bool   `json:"wait,omitempty"`    // return after playback
	Client    string `json:"client,omitempty"`  // claude, codex, ...: picks [clients] preset
	PID       uint32 `json:"pid,omitempty"`     // the agent's process, for dictation
	Project   string `json:"project,omitempty"` // its working directory name, for routing
}

type ControlArgs struct {
	Action  string `json:"action"`
	Preset  string `json:"preset,omitempty"`
	Client  string `json:"client,omitempty"`
	PID     uint32 `json:"pid,omitempty"`
	Project string `json:"project,omitempty"`
	Name    string `json:"name,omitempty"` // register: what the user calls this session
	// Character: set_character's choice from list_characters.
	Character string `json:"character,omitempty"`
	// Session: transcript's filter, a session's or character's name, or "me".
	Session string `json:"session,omitempty"`
	Limit   Count  `json:"limit,omitempty"` // transcript: how many lines, newest
}

type ListenArgs struct {
	Prompt      string  `json:"prompt,omitempty"`
	WaitSeconds float64 `json:"wait_seconds,omitempty"`
	MaxSeconds  float64 `json:"max_seconds,omitempty"`
	Client      string  `json:"client,omitempty"`
	PID         uint32  `json:"pid,omitempty"`
	Project     string  `json:"project,omitempty"`
}

type CloneArgs struct {
	Action          string  `json:"action"`
	Name            string  `json:"name,omitempty"`
	ClipPath        string  `json:"clip_path,omitempty"`
	Transcript      string  `json:"transcript,omitempty"`
	Engine          string  `json:"engine,omitempty"`
	RightsConfirmed bool    `json:"rights_confirmed,omitempty"`
	Rights          string  `json:"rights,omitempty"` // who confirmed; CLI only
	Text            string  `json:"text,omitempty"`
	Replace         bool    `json:"replace,omitempty"`
	Seconds         float64 `json:"seconds,omitempty"` // record: length of the recording
}

// API is what front ends call; Service and the daemon client implement it.
type API interface {
	Speak(ctx context.Context, a SpeakArgs) (any, error)
	Control(ctx context.Context, a ControlArgs) (any, error)
	Listen(ctx context.Context, a ListenArgs) (any, error)
	Clone(ctx context.Context, a CloneArgs) (any, error)
	Agent(ctx context.Context, a AgentArgs) (any, error)
	Dictate(ctx context.Context, a DictateArgs) (any, error)
	Mod(ctx context.Context, a ModArgs) (any, error)
	Session(ctx context.Context, a SessionArgs) (any, error)
}

// DictateArgs hands text to the agent as if the user had dictated it.
type DictateArgs struct {
	Text string `json:"text"`
}

// Dictate delivers text like the dictation hotkey does (`voice dictate`).
func (s *Service) Dictate(_ context.Context, a DictateArgs) (any, error) {
	if s.Deliver == nil {
		return nil, userErr("dictation runs in the background process")
	}
	if strings.TrimSpace(a.Text) == "" {
		return nil, userErr("dictate needs text")
	}
	return s.Deliver(a.Text), nil
}

// UserError is reported to the caller as is.
type UserError struct{ Msg string }

func (e UserError) Error() string { return e.Msg }

func userErr(format string, args ...any) error { return UserError{fmt.Sprintf(format, args...)} }

type Service struct {
	Speaker  *core.Speaker
	Config   *preset.Config
	Listener *listen.Listener
	Agents   *Agents // agent processes that used voice; nil records nothing
	// Deliver passes dictated text to an agent and says where it went.
	Deliver func(text string) string
	// DeliverTo passes a message to one agent the way dictation reaches it,
	// never by typing into another window; ok is false when it took none.
	DeliverTo func(pid uint32, text string) (report string, ok bool)

	recMu   sync.Mutex
	recStop chan struct{} // closed by the clone action stop: the recording ends early
}

func (s *Service) presetFor(explicit, client string) string {
	if explicit != "" || client == "" {
		return explicit
	}
	for name, p := range s.Config.Clients {
		if strings.Contains(strings.ToLower(client), strings.ToLower(name)) {
			return p
		}
	}
	return ""
}

func (s *Service) Speak(ctx context.Context, a SpeakArgs) (any, error) {
	s.Agents.Seen(a.PID, a.Client, a.Project)
	if s.Agents.NoSpeech(a.PID) {
		return nil, userErr("muted by the user for this session; stay silent until they unmute it")
	}
	t, err := s.Speaker.Speak(core.Request{
		Text: a.Text, Intent: a.Intent, Lang: a.Lang, Interrupt: a.Interrupt,
		Preset: s.presetFor(dirOr(a.Preset, s.characterPreset(a.PID)), a.Client), Cache: a.Cache,
	})
	if errors.Is(err, core.ErrMuted) {
		return nil, userErr("muted by the user; stay silent until they unmute")
	}
	if err != nil {
		return nil, UserError{err.Error()}
	}
	s.Agents.Record(Line{Kind: "said", ID: t.ID, PID: a.PID, Source: a.Source, Text: a.Text})
	if a.Source != "hook" {
		core.MarkAsked(a.PID) // its end-of-turn announcement and question reading stay quiet
	}
	if !a.Wait {
		out := map[string]any{"id": t.ID, "queue_position": t.Position}
		if t.Note != "" {
			out["voice_note"] = t.Note + ". Tell the user once."
		}
		if persona, changed := s.takePersona(a.PID, false); changed {
			out["note"] = "The user changed your character. From now on: " + persona
		}
		return out, nil
	}
	select {
	case res := <-t.Done:
		if res.Err != nil {
			return nil, UserError{res.Err.Error()}
		}
		out := map[string]any{"id": t.ID, "engine": res.Engine}
		if t.Note != "" {
			out["voice_note"] = t.Note + ". Tell the user once."
		}
		return out, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Service) Control(_ context.Context, a ControlArgs) (any, error) {
	s.Agents.Seen(a.PID, a.Client, a.Project)
	sp := s.Speaker
	switch a.Action {
	case "register":
		// Seen above already recorded the caller.
		if a.PID == 0 {
			return nil, userErr("voice could not tell which process you are; dictation reaches you after your first voice call or hook")
		}
		// A character's name the list no longer has (renamed, deleted) gives way to a free character;
		// a name the user chose stays.
		if _, ok := s.Config.Character(s.Agents.Name(a.PID)); !ok && a.Name == "" && !s.Agents.Custom(a.PID) && len(s.Config.Characters) > 0 {
			a.Name = s.Agents.FreeName()
		}
		name := s.Agents.SetName(a.PID, a.Name)
		out := fmt.Sprintf("registered for dictation as %s: the user starts a dictated message with \"%s, ...\" to reach only you (tell them your name if they ask)", name, name)
		if persona, _ := s.takePersona(a.PID, true); persona != "" {
			out += ". Your character: " + persona
		}
		return out, nil
	case "set_character":
		// The agent takes another character from the list, e.g. when the user asks.
		if a.PID == 0 {
			return nil, userErr("voice could not tell which process you are; call register first")
		}
		if a.Character == "" {
			a.Character = a.Name // agents reach for "name" as often as "character"
		}
		if a.Character == "" {
			return nil, userErr("set_character needs character: a name from list_characters")
		}
		ch, ok := s.Config.Character(a.Character)
		if !ok {
			return nil, userErr("no character %q; list_characters shows them", a.Character)
		}
		name := s.Agents.SetName(a.PID, ch.Name) // as the list spells it

		persona, _ := s.takePersona(a.PID, true)
		if persona == "" {
			return fmt.Sprintf("the user calls you %s now; characters are switched off in the settings, so keep your usual tone", name), nil
		}
		return "From now on: " + persona, nil
	case "transcript":
		return s.Agents.TranscriptFor(a.PID, a.Session, int(a.Limit)), nil
	case "list_characters":
		rows := [][]string{}
		for _, ch := range s.Config.Characters {
			rows = append(rows, []string{ch.Name, strings.Join(ch.Traits, ", "), ch.Description})
		}
		return map[string]any{"you": s.Agents.Name(a.PID), "personas_on": s.personasOn(), "columns": []string{"name", "traits", "description"}, "rows": rows}, nil
	case "status":
		st := sp.Status()
		out := map[string]any{"preset": st.Preset, "muted": st.Muted, "speaking": st.Speaking, "queued": st.Queued}
		if st.LastError != "" {
			out["last_error"] = st.LastError
		}
		return out, nil
	case "stop":
		sp.Stop()
		return "stopped", nil
	case "mute":
		sp.SetMuted(true)
		return "muted", nil
	case "unmute":
		sp.SetMuted(false)
		return "unmuted", nil
	case "list_presets":
		rows := [][]string{}
		for _, name := range s.Config.PresetNames() {
			if r, err := s.Config.Resolve(name); err == nil {
				rows = append(rows, []string{name, r.Description})
			}
		}
		return map[string]any{"active": sp.Status().Preset, "columns": []string{"name", "description"}, "rows": rows}, nil
	case "set_preset":
		if a.Preset == "" {
			return nil, userErr("set_preset needs preset; call list_presets for names")
		}
		if err := sp.SetPreset(a.Preset); err != nil {
			return nil, UserError{err.Error()}
		}
		return "preset " + a.Preset, nil
	}
	return nil, userErr("unknown action %q", a.Action)
}

func (s *Service) Listen(ctx context.Context, a ListenArgs) (any, error) {
	if s.Listener == nil || !s.Listener.Available() {
		return nil, userErr("speech recognition is not installed; run `voice models pull vad asr-parakeet`")
	}
	s.Agents.Seen(a.PID, a.Client, a.Project)
	if a.Prompt != "" {
		res, err := s.Speak(ctx, SpeakArgs{Text: a.Prompt, Intent: core.IntentQuestion, Wait: true, Client: a.Client, PID: a.PID, Project: a.Project})
		if err != nil {
			return res, err
		}
	} else {
		// Do not record the agent's own queued speech.
		for st := s.Speaker.Status(); st.Speaking || st.Queued > 0; st = s.Speaker.Status() {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	secs := func(v, def float64) time.Duration {
		if v <= 0 {
			v = def
		}
		return time.Duration(min(v, 45) * float64(time.Second))
	}
	res, err := s.Listener.Listen(ctx, listen.Options{Wait: secs(a.WaitSeconds, 15), MaxSpeech: secs(a.MaxSeconds, 30)})
	if err != nil {
		return nil, UserError{err.Error()}
	}
	res.Text = VocabularyFor(s.Config, s.Agents).Correct(res.Text)
	return res, nil
}

func (s *Service) Clone(ctx context.Context, a CloneArgs) (any, error) {
	need := func(fields ...string) error {
		for _, f := range fields {
			if (f == "name" && a.Name == "") || (f == "clip_path" && a.ClipPath == "") || (f == "transcript" && a.Transcript == "") {
				return userErr("%s needs %s", a.Action, f)
			}
		}
		return nil
	}
	switch a.Action {
	case "stop":
		// Ends a recording early; what was recorded so far is cloned.
		s.recMu.Lock()
		defer s.recMu.Unlock()
		if s.recStop == nil {
			return "no recording to stop", nil
		}
		close(s.recStop)
		s.recStop = nil
		return "stopping", nil
	case "record":
		// Records the clip from the microphone, checks it, then adds it like "add".
		if err := need("name"); err != nil {
			return nil, err
		}
		if err := clones.ValidName(a.Name); err != nil {
			return nil, UserError{err.Error()}
		}
		// Before the user speaks for half a minute, not after.
		if _, err := clones.Get(a.Name); err == nil && !a.Replace {
			return nil, userErr("a clone called %q exists; pick another name or pass replace=true", a.Name)
		}
		if !a.RightsConfirmed {
			return nil, userErr("not recorded:ask the user whether they have the right to use this voice, then retry with rights_confirmed=true")
		}
		if s.Listener == nil {
			return nil, userErr("recording needs the background process")
		}
		secs := a.Seconds
		if secs <= 0 {
			secs = 12
		}
		secs = max(6, min(secs, 30))
		const rate = 24000
		stop := make(chan struct{})
		s.recMu.Lock()
		s.recStop = stop
		s.recMu.Unlock()
		pcm, err := s.Listener.Record(ctx, secs, rate, stop)
		s.recMu.Lock()
		if s.recStop == stop {
			s.recStop = nil
		}
		s.recMu.Unlock()
		if err != nil {
			return nil, UserError{"recording failed: " + err.Error()}
		}
		if got := float64(len(pcm)) / rate; got < 4 {
			return nil, userErr("stopped after %.0f s: record at least 5 s of speech, ideally 10-15", got)
		}
		problems, warnings := clones.Check(pcm, rate)
		if len(problems) > 0 {
			return nil, userErr("recording not usable: %s; record again", strings.Join(problems, "; "))
		}
		clip := filepath.Join(os.TempDir(), "voice-record-"+a.Name+".wav")
		if err := os.WriteFile(clip, audio.EncodeWAV(pcm, rate), 0o644); err != nil {
			return nil, err
		}
		defer os.Remove(clip)
		add := a
		add.Action, add.ClipPath = "add", clip
		res, err := s.Clone(ctx, add)
		if m, ok := res.(map[string]any); ok && len(warnings) > 0 {
			m["quality"] = warnings
		}
		return res, err
	case "add":
		if err := need("name", "clip_path"); err != nil {
			return nil, err
		}
		if _, ok := s.Config.Engines[a.Engine]; a.Engine != "" && !ok {
			return nil, userErr("unknown engine %q", a.Engine)
		}
		if !a.RightsConfirmed {
			return nil, userErr("not cloned: ask the user whether they have the right to use this voice, then retry with rights_confirmed=true")
		}
		rights := a.Rights
		if rights == "" {
			rights = "confirmed by the user to the agent"
		}
		p, err := clones.Add(a.Name, a.ClipPath, a.Transcript, a.Engine, rights, a.Replace)
		if err != nil {
			return nil, UserError{err.Error()}
		}
		p = s.autoTranscript(p)
		s.Config.SetPreset(core.ClonePresetName(p.Name), core.ClonePreset(p))
		out := map[string]any{"name": p.Name, "preset": core.ClonePresetName(p.Name), "engine": p.Engine, "seconds": p.Duration, "transcript": p.Text}
		if NeedsTranscript(p.Engine) && p.Text == "" {
			out["warning"] = p.Engine + " needs a transcript; call set_transcript"
		}
		return out, nil
	case "list":
		rows := [][]any{}
		for _, p := range clones.List() {
			rows = append(rows, []any{p.Name, core.ClonePresetName(p.Name), p.Engine, p.Duration, p.Text != "", p.Source})
		}
		return map[string]any{"columns": []string{"name", "preset", "engine", "seconds", "transcript", "source"}, "rows": rows}, nil
	case "preview":
		if err := need("name"); err != nil {
			return nil, err
		}
		if _, err := clones.Get(a.Name); err != nil {
			return nil, UserError{err.Error()}
		}
		sample := a.Text
		if sample == "" {
			sample = "Hi, this is how I sound now. I'm ready for the next task."
		}
		return s.Speak(ctx, SpeakArgs{Text: sample, Preset: core.ClonePresetName(a.Name), Lang: "en"})
	case "set_transcript":
		if err := need("name", "transcript"); err != nil {
			return nil, err
		}
		if err := clones.SetText(a.Name, a.Transcript); err != nil {
			return nil, UserError{err.Error()}
		}
		return "transcript stored", nil
	case "remove":
		if err := need("name"); err != nil {
			return nil, err
		}
		if err := clones.Remove(a.Name); err != nil {
			return nil, UserError{err.Error()}
		}
		name := core.ClonePresetName(a.Name)
		if s.Speaker.Status().Preset == name {
			s.Speaker.SetPreset(s.Config.DefaultPreset)
		}
		s.Config.DeletePreset(name)
		return "removed " + a.Name, nil
	}
	return nil, userErr("unknown action %q", a.Action)
}

// NeedsTranscript reports whether a cloning engine needs the clip's
// transcript: ZipVoice, and the GPU backends sidecar/backends.json marks so.
func NeedsTranscript(engine string) bool {
	if engine == "zipvoice" {
		return true
	}
	b, ok := sidecar.Find(engine)
	return ok && b.Transcript
}

// autoTranscript fills a missing transcript with speech recognition when a
// model is installed; ZipVoice needs one and it helps other engines.
func (s *Service) autoTranscript(p clones.Profile) clones.Profile {
	if p.Text != "" || s.Listener == nil || !s.Listener.Available() {
		return p
	}
	if text, _, err := s.Listener.TranscribeFile(p.Ref); err == nil && text != "" && clones.SetText(p.Name, text) == nil {
		p.Text = text
	}
	return p
}

func dirOr(v, def string) string {
	if v != "" {
		return v
	}
	return def
}
