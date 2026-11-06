// Package service implements voice's operations once, for every front end:
// the background process runs them, and the MCP server, hooks and CLI reach
// them through the daemon client. Results are JSON-ready values; a
// UserError is a message for the caller, not a failure of voice.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"voice/internal/audio"
	"voice/internal/clones"
	"voice/internal/core"
	"voice/internal/engine"
	"voice/internal/engine/httpengine"
	"voice/internal/hooks"
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
	Wait      bool   `json:"wait,omitempty"` // return after playback
	// Lines: a script instead of Text, each line in its own voice (a
	// character or a preset; empty: Voice or the session's), spoken as one
	// flowing utterance.
	Lines SpeakLines `json:"lines,omitempty"`
	// Mode: how a script is heard, play, render or auto (core.Request.Mode).
	Mode string `json:"mode,omitempty"`
	// Mood: how text is said (core.Moods), where the voice's engine can show
	// it; a script's lines carry their own.
	Mood string `json:"mood,omitempty"`
	// Scene: where and when the lines take place, for the reading window and
	// its themes; nothing of it is spoken.
	Scene *SceneInfo `json:"scene,omitempty"`
	// Voice: for this call only, a character's name (its preset) or a
	// preset's name; empty uses the session's character or the default.
	Voice string `json:"voice,omitempty"`
	// Turn: an id for one reply sent in pieces; a piece with the same turn
	// joins the one before while it is queued or playing (core.Request.Turn).
	Turn string `json:"turn,omitempty"`
	// Display: what the transcript and the windows show for Text, when it is
	// more than is said (a role-play reply's narration around its dialogue);
	// only Text is spoken.
	Display string `json:"display,omitempty"`
	// SaveTo: render into this file (an absolute .wav or .mp3 path) instead
	// of saying it; the call returns once the file is written.
	SaveTo string `json:"save_to,omitempty"`
	// Announce: an agent event to announce (a hook's) instead of Text; what
	// is said is composed here, in the language the session's voice speaks.
	Announce *hooks.Event `json:"announce,omitempty"`
	Client   string       `json:"client,omitempty"`  // claude, codex, ...: picks [clients] preset
	PID      uint32       `json:"pid,omitempty"`     // the agent's process, for dictation
	Project  string       `json:"project,omitempty"` // its working directory name, for routing
}

type ControlArgs struct {
	Action  string `json:"action"`
	Preset  string `json:"preset,omitempty"`
	Client  string `json:"client,omitempty"`
	PID     uint32 `json:"pid,omitempty"`
	Project string `json:"project,omitempty"`
	Name    string `json:"name,omitempty"` // register: what the user calls this session
	// KeepWarm: register asks voice to keep this session's GPU voice loaded
	// while the session runs, so its first sentence is never a cold start.
	KeepWarm bool `json:"keep_warm,omitempty"`
	// Character: set_character's choice from list_characters.
	Character string `json:"character,omitempty"`
	// Session: transcript's filter, a session's or character's name, or "me".
	Session string `json:"session,omitempty"`
	Limit   Count  `json:"limit,omitempty"` // transcript: how many lines, newest
	// Style: set_style's choice, short (the default) or narrative.
	Style string `json:"style,omitempty"`
	// ID: status of one utterance, a speak answer's id.
	ID string `json:"id,omitempty"`
	// Theme: set_theme's choice from list_themes; "" goes back to the user's.
	Theme string `json:"theme,omitempty"`
	// To and Text: send's message to another session, by its name.
	To   string `json:"to,omitempty"`
	Text string `json:"text,omitempty"`
}

// installedThemes are the reading window's themes: folders in the data
// folder's themes with a theme.css.
func installedThemes() []string {
	dir := filepath.Join(core.DataDir(), "themes")
	out := []string{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "theme.css")); e.IsDir() && err == nil {
			out = append(out, e.Name())
		}
	}
	return out
}

// narrativeChars is how long one line may be in the narrative style.
const narrativeChars = 4000

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
	// Description: design's voice in words ("a raspy older woman, slow and dry").
	Description string `json:"description,omitempty"`
	Lang        string `json:"lang,omitempty"` // design: the sample's language (default en)
	// Group: the user's group a voice goes in (group; add, record and design
	// file the new voice there), or the group to rename or remove.
	Group    string `json:"group,omitempty"`
	NewGroup string `json:"new_group,omitempty"` // rename_group: the group's new name
	NewName  string `json:"new_name,omitempty"`  // rename: the voice's new name
}

// DesignEngine makes voices from a description (sidecar/backends.json).
const DesignEngine = "qwen3_design"

const designEngine = DesignEngine

// designSample is what a designed voice reads once; it becomes the clone's
// recording and transcript. About ten seconds, varied in tone.
const designSample = "Hello there. This is a new voice, made from a short description. I can read you a story, answer a question, or let you know when the work is done. Shall we begin?"

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
	// ListenView, when set, shows on screen that an agent (who) opened the
	// microphone, as dictation is shown; the signs it returns follow it.
	ListenView func(who string) ListenSigns
	Speaker    *core.Speaker
	Config     *preset.Config
	Listener   *listen.Listener
	Agents     *Agents // agent processes that used voice; nil records nothing
	// Deliver passes dictated text to an agent and says where it went.
	Deliver func(text string) string
	// DeliverTo passes a message to one agent the way dictation reaches it,
	// never by typing into another window; ok is false when it took none.
	DeliverTo func(pid uint32, text string) (report string, ok bool)
	// RenamePreset makes the stored settings that name the voice preset old
	// (characters, the default voice) name new, and applies them; nil skips it.
	RenamePreset func(old, new string) error

	recMu   sync.Mutex
	recStop chan struct{} // closed by the clone action stop: the recording ends early
}

// SpeakLines is a script's lines. A client whose tool list predates them may
// send the list as a JSON string; it is read the same.
type SpeakLines []SpeakLine

func (l *SpeakLines) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		b = []byte(s)
	}
	var lines []SpeakLine
	if err := json.Unmarshal(b, &lines); err != nil {
		return fmt.Errorf("lines: a list of {text, voice}: %w", err)
	}
	*l = lines
	return nil
}

// SpeakLine is one line of a script.
type SpeakLine struct {
	Text  string `json:"text,omitempty"`
	Voice string `json:"voice,omitempty"`
	// Pause: seconds of silence after the line; alone, a break.
	Pause float64 `json:"pause,omitempty"`
	// What the reading window and its themes show besides the words; none of
	// it is spoken. Kind: dialogue (the default), narration or thought. Mood:
	// how it is said. Action: a stage direction ("leans on the bar"); a line
	// with an action and no text is only shown. To: whom it is said to.
	Kind   string `json:"kind,omitempty"`
	Mood   string `json:"mood,omitempty"`
	Action string `json:"action,omitempty"`
	To     string `json:"to,omitempty"`
}

// SceneInfo is where and when a scene takes place. Background names a
// picture a reading-window theme may have for it ("tavern", "night-road").
type SceneInfo struct {
	Title      string `json:"title,omitempty"`
	Location   string `json:"location,omitempty"`
	Time       string `json:"time,omitempty"`
	Background string `json:"background,omitempty"`
}

// UnmarshalJSON takes the object, or the object as a JSON string (some
// clients send nested arguments so).
func (s *SceneInfo) UnmarshalJSON(b []byte) error {
	var str string
	if json.Unmarshal(b, &str) == nil {
		b = []byte(str)
	}
	type plain SceneInfo
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return fmt.Errorf("scene: an object with title, location, time and background: %w", err)
	}
	*s = SceneInfo(v)
	return nil
}

// script turns a call's lines into the speaker's, each with its preset, and
// the text the transcript keeps ("Clara: ..."). Without lines it is nil.
func (s *Service) script(a SpeakArgs) ([]core.ScriptLine, string, error) {
	if len(a.Lines) == 0 {
		return nil, core.StripTags(a.Text), nil
	}
	own, err := s.speakPreset(a)
	if err != nil {
		return nil, "", err
	}
	var out []core.ScriptLine
	var said []string
	for _, l := range a.Lines {
		if strings.TrimSpace(l.Text) == "" {
			if l.Pause > 0 {
				out = append(out, core.ScriptLine{Pause: l.Pause}) // a break alone
			}
			continue
		}
		p := own
		if l.Voice != "" {
			if p, err = s.speakPreset(SpeakArgs{Voice: l.Voice}); err != nil {
				return nil, "", err
			}
			said = append(said, l.Voice+": "+core.StripTags(l.Text))
		} else {
			said = append(said, core.StripTags(l.Text))
		}
		out = append(out, core.ScriptLine{Text: l.Text, Preset: p, Pause: l.Pause, Mood: l.Mood})
	}
	if len(said) == 0 {
		return nil, "", userErr("lines has no text")
	}
	return out, strings.Join(said, " "), nil
}

// scene is a call's lines as the transcript keeps them: who says each, in
// which voice, and the break after it. A break alone adds to the line before.
func (s *Service) scene(a SpeakArgs, lines []core.ScriptLine) []SceneLine {
	var out []SceneLine
	k := 0 // lines has a line for each with text or a pause, in order
	for _, l := range a.Lines {
		text := strings.TrimSpace(l.Text)
		if text == "" && strings.TrimSpace(l.Action) == "" {
			if l.Pause > 0 {
				if n := len(out) - 1; n >= 0 {
					out[n].Pause += l.Pause
				}
				k++
			}
			continue
		}
		sl := SceneLine{Speaker: l.Voice, Text: core.StripTags(text), Pause: l.Pause, Kind: l.Kind, Mood: l.Mood, Action: strings.TrimSpace(l.Action), To: l.To}
		if text == "" {
			// Only shown: a stage direction, with the break after it if any.
			if l.Pause > 0 {
				k++
			}
			out = append(out, sl)
			continue
		}
		for _, ch := range s.Config.Characters {
			if strings.EqualFold(ch.Name, l.Voice) {
				sl.Speaker = ch.Name
			}
		}
		if k < len(lines) {
			sl.Voice = lines[k].Preset
		}
		k++
		out = append(out, sl)
	}
	return out
}

// speakPreset is the preset a call speaks with: the voice it names (a
// character or a preset), else the session's character, the client's
// preset or the default.
func (s *Service) speakPreset(a SpeakArgs) (string, error) {
	if v := strings.TrimSpace(a.Voice); v != "" {
		if ch, ok := s.Config.Character(v); ok {
			return ch.Preset, nil
		}
		if s.Config.HasPreset(v) {
			return v, nil
		}
		var names []string
		for _, ch := range s.Config.Characters {
			names = append(names, ch.Name)
		}
		return "", userErr("no character or preset %q; characters: %s; presets: voice_control list_presets", v, strings.Join(names, ", "))
	}
	return s.presetFor(dirOr(a.Preset, s.characterPreset(a.PID)), a.Client), nil
}

// speakingAs is the character a call speaks as: the one it names, else the
// session's own when its character's voice is used; empty for a bare preset.
func (s *Service) speakingAs(a SpeakArgs) string {
	if v := strings.TrimSpace(a.Voice); v != "" {
		if ch, ok := s.Config.Character(v); ok {
			return ch.Name
		}
		return ""
	}
	if a.Preset == "" && s.characterPreset(a.PID) != "" {
		return s.Agents.Name(a.PID)
	}
	return ""
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
	if a.SaveTo != "" {
		return s.saveSpeech(ctx, a)
	}
	if s.Agents.NoSpeech(a.PID) {
		return nil, userErr("muted by the user for this session; stay silent until they unmute it")
	}
	if a.Lang == "" {
		a.Lang = s.speakLang(a)
	}
	if a.Announce != nil {
		a.Text, a.Cache = hooks.Compose(s.Config.Hooks, *a.Announce, a.Lang, s.phraseLang(a.Lang))
		a.Announce = nil
		if a.Text == "" {
			return map[string]any{"said": "", "quiet": "no phrase for this event"}, nil
		}
	}
	preset, err := s.speakPreset(a)
	if err != nil {
		return nil, err
	}
	lines, said, err := s.script(a)
	if err != nil {
		return nil, err
	}
	maxChars := s.speechChars(a.PID) // a narrative, or most of a reply said aloud, is not cut short
	t, err := s.Speaker.Speak(core.Request{
		Text: a.Text, Intent: a.Intent, Lang: a.Lang, Interrupt: a.Interrupt,
		Preset: preset, Cache: a.Cache, Lines: lines, MaxChars: maxChars, Mood: a.Mood, Mode: a.Mode, Owner: a.PID, Turn: a.Turn,
	})
	if errors.Is(err, core.ErrMuted) {
		return nil, userErr("muted by the user; stay silent until they unmute")
	}
	if err != nil {
		return nil, UserError{err.Error()}
	}
	// What the windows show: the display text when the call has one.
	shown := said
	if d := strings.TrimSpace(core.StripTags(a.Display)); d != "" && len(lines) == 0 {
		shown = d
	}
	if t.Appended {
		s.Agents.Grow(t.ID, shown, said) // one reply, one transcript line
	} else {
		l := Line{Kind: "said", ID: t.ID, PID: a.PID, Source: a.Source, Text: shown, Scene: len(lines) > 0, Lines: s.scene(a, lines), Setting: a.Scene, Style: s.Agents.Style(a.PID)}
		if shown != said {
			l.Spoken = said
		}
		s.Agents.Record(l)
	}
	if a.Source != "hook" {
		core.MarkAsked(a.PID) // its end-of-turn announcement and question reading stay quiet
	}
	// Who and what will say it, so a caller knows what was chosen.
	who := map[string]any{"voice": strings.Join(t.Voices, ", ")}
	if as := s.speakingAs(a); as != "" && len(a.Lines) == 0 {
		who["as"] = as
	}
	if !a.Wait || t.Appended {
		state := "queued"
		if t.Appended {
			state = "appended" // joined its turn's utterance; status follows that id
		}
		out := map[string]any{"id": t.ID, "state": state, "queue_position": t.Position}
		maps.Copy(out, who)
		if t.Note != "" {
			out["voice_note"] = t.Note + ". Tell the user once."
		}
		var notes []string
		if persona, changed := s.takePersona(a.PID, false); changed {
			notes = append(notes, "The user changed your character. From now on: "+persona)
		}
		if a.Source != "hook" {
			if l := s.takeLangNote(a.PID, false); l != "" {
				notes = append(notes, l)
			}
			if l := s.takeAloudNote(a.PID, false); l != "" {
				notes = append(notes, l)
			}
		}
		if len(notes) > 0 {
			out["note"] = strings.Join(notes, " ")
		}
		if a.Source == "hook" {
			out["said"] = a.Text
		}
		return out, nil
	}
	select {
	case res := <-t.Done:
		if res.Err != nil {
			return nil, UserError{res.Err.Error()}
		}
		out := map[string]any{"id": t.ID, "state": "done", "engine": res.Engine}
		maps.Copy(out, who)
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
		out = strings.TrimSpace(out + ". " + s.takeLangNote(a.PID, true) + " " + s.takeAloudNote(a.PID, true))
		if warm := s.setKeepWarm(a.PID, a.KeepWarm); warm != "" {
			out += " " + warm
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
		// How much it says aloud only when the new character changes it (a switch back to short included).
		return strings.TrimSpace("From now on: " + persona + " " + s.takeLangNote(a.PID, true) + " " + s.takeAloudNote(a.PID, false)), nil
	case "transcript":
		return s.Agents.TranscriptFor(a.PID, a.Session, int(a.Limit)), nil
	case "list_characters":
		rows := [][]string{}
		for _, ch := range s.Config.Characters {
			rows = append(rows, []string{ch.Name, strings.Join(ch.Traits, ", "), ch.Description})
		}
		return map[string]any{"you": s.Agents.Name(a.PID), "personas_on": s.personasOn(), "columns": []string{"name", "traits", "description"}, "rows": rows}, nil
	case "status":
		if a.ID != "" {
			t, ok := sp.Track(a.ID)
			if !ok {
				return nil, userErr("no utterance %q: voice keeps the last 100, and ids start again when voice restarts", a.ID)
			}
			return t, nil
		}
		st := sp.Status()
		out := map[string]any{"preset": st.Preset, "muted": st.Muted, "speaking": st.Speaking, "queued": st.Queued}
		if st.LastError != "" {
			out["last_error"] = st.LastError
		}
		return out, nil
	case "stop":
		// A session stops its own speech; others' goes on. A caller voice
		// cannot tell apart (the command line) stops everything.
		if a.PID == 0 {
			sp.Stop()
			return "stopped", nil
		}
		if n := sp.StopOwner(a.PID); n == 0 {
			return "nothing of yours was playing or queued", nil
		}
		return "stopped your speech; other sessions' goes on", nil
	case "set_style":
		switch a.Style {
		case StyleShort:
			s.Agents.SetStyle(a.PID, StyleShort)
			return "short style: one or two short spoken sentences per reply; details stay on screen", nil
		case StyleNarrative:
			s.Agents.SetStyle(a.PID, StyleNarrative)
			return "narrative style: speech is the content now. Say each reply aloud in full as spoken prose (a story, a scene); when characters speak, pass the whole scene in one voice_scene call with lines, each in its voice. Code, lists and links still stay on screen. set_style short ends it.", nil
		}
		return nil, userErr("style must be short or narrative")
	case "send":
		return s.sessionSend(SessionArgs{To: a.To, Text: a.Text}, a.PID)
	case "list_themes":
		return map[string]any{"themes": installedThemes(), "note": "set_theme with one gives this session's messages that look in the user's reading window; the user's own theme stays theirs"}, nil
	case "set_theme":
		name := strings.TrimSpace(a.Theme)
		if name != "" && !slices.Contains(installedThemes(), name) {
			return nil, userErr("no theme %q; installed: %s (list_themes)", name, strings.Join(installedThemes(), ", "))
		}
		s.Agents.SetTheme(a.PID, name)
		if name == "" {
			return "this session uses the user's theme again", nil
		}
		return "this session's messages use the " + name + " theme when the reading window shows this session", nil
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
				x := sp.PresetExpressive(name)
				moods := x.Emotion
				if m := x.MoodNames(); len(m) > 0 && x.Emotion != "instruct" {
					moods += ": " + strings.Join(m, ", ")
				}
				rows = append(rows, []string{name, r.Description, strings.Join(x.SoundNames(), " "), moods})
			}
		}
		return map[string]any{"active": sp.Status().Preset, "columns": []string{"name", "description", "sounds", "moods"}, "rows": rows,
			"tags": "Write {sound} in text for a sound (" + strings.Join(core.Sounds, ", ") + ") and {pause} or {pause:1.5} for silence; a voice makes the sounds in its column and drops the rest, never reading them aloud. mood (" + strings.Join(core.Moods, ", ") + ") works as its column says."}, nil
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
	opts := listen.Options{Wait: secs(a.WaitSeconds, 15), MaxSpeech: secs(a.MaxSeconds, 30)}
	// The user must see when an agent hears them: the same signs as their own
	// dictation (the corner ring with their voice, their words as heard).
	if view := s.ListenView; view != nil {
		v := view(dirOr(s.Agents.Name(a.PID), "an agent"))
		opts.Level, opts.Recorded = v.Level, v.Recorded
		opts.Partial = func(t string) { v.Partial(VocabularyFor(s.Config, s.Agents).Correct(t)) }
		defer v.End()
	}
	res, err := s.Listener.Listen(ctx, opts)
	if err != nil {
		return nil, UserError{err.Error()}
	}
	res.Text = VocabularyFor(s.Config, s.Agents).Correct(res.Text)
	return res, nil
}

// ListenSigns are what the screen shows while an agent listens: the
// microphone level, the end of recording, the words heard so far, the end.
type ListenSigns struct {
	Level    func(float64)
	Recorded func()
	Partial  func(string)
	End      func()
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
		if core.ClonePresetTaken(s.Config, a.Name) {
			return nil, userErr("%q is already a voice preset; pick another name for the cloned voice", a.Name)
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
		if core.ClonePresetTaken(s.Config, a.Name) {
			return nil, userErr("%q is already a voice preset; pick another name for the cloned voice", a.Name)
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
		clip, text := a.ClipPath, a.Transcript
		// A reference description (dataset tools write them): the recording
		// and what it says, in one JSON file.
		if strings.EqualFold(filepath.Ext(clip), ".json") {
			c, t, err := clones.Reference(clip)
			if err != nil {
				return nil, UserError{err.Error()}
			}
			clip = c
			if text == "" {
				text = t
			}
		}
		p, err := clones.Add(a.Name, clip, text, a.Engine, rights, a.Replace)
		if err != nil {
			return nil, UserError{err.Error()}
		}
		p = s.autoTranscript(p)
		s.Config.SetPreset(core.ClonePresetName(p.Name), core.ClonePreset(p))
		out := map[string]any{"name": p.Name, "preset": core.ClonePresetName(p.Name), "engine": p.Engine, "seconds": p.Duration, "transcript": p.Text}
		if strings.TrimSpace(a.Group) != "" {
			if g, err := clones.SetGroup(p.Name, a.Group); err != nil {
				out["warning"] = "not filed in a group: " + err.Error()
			} else {
				out["group"] = g
			}
		} else if p.Group != "" {
			out["group"] = p.Group
		}
		// add/record: the user's words about the voice (design's description
		// is what it was designed from, stored apart).
		if d := strings.TrimSpace(a.Description); d != "" {
			if err := clones.SetDescription(p.Name, d); err == nil {
				if q, err := clones.Get(p.Name); err == nil {
					p = q
					s.Config.SetPreset(core.ClonePresetName(p.Name), core.ClonePreset(p))
				}
			}
		}
		if NeedsTranscript(p.Engine) && p.Text == "" {
			out["warning"] = p.Engine + " needs a transcript; call set_transcript"
		}
		// An engine that keeps voice prompts makes one now: speech then needs
		// no recording.
		if KeepsPrompts(p.Engine) && p.Text != "" {
			if info, err := s.makePrompt(ctx, p); err != nil {
				out["warning"] = "the voice works from its recording; its prompt was not made: " + err.Error()
			} else {
				out["prompt"] = info
			}
		}
		return out, nil
	case "design":
		// A new voice from a description: the design model reads a sample
		// once, and the sample becomes an ordinary cloned voice, so the voice
		// is the same in every line. It plays a preview; remove drops it.
		if err := need("name"); err != nil {
			return nil, err
		}
		desc := strings.TrimSpace(a.Description)
		if desc == "" {
			return nil, userErr("design needs description: the voice in words, e.g. \"a raspy older woman, slow and dry, faint British accent\"")
		}
		if err := clones.ValidName(a.Name); err != nil {
			return nil, UserError{err.Error()}
		}
		if _, err := clones.Get(a.Name); err == nil && !a.Replace {
			return nil, userErr("a voice called %q exists; pick another name or pass replace=true", a.Name)
		}
		if core.ClonePresetTaken(s.Config, a.Name) {
			return nil, userErr("%q is already a voice preset; pick another name for the designed voice", a.Name)
		}
		target := a.Engine
		if target == "" {
			target = "qwen3"
		}
		sample := strings.TrimSpace(a.Text)
		if sample == "" {
			sample = designSample
		}
		lang := a.Lang
		if lang == "" {
			lang = "en"
		}
		pcm, rate, err := s.design(ctx, desc, sample, lang)
		if err != nil {
			card := "a graphics card"
			if b, ok := sidecar.Find(DesignEngine); ok {
				card = b.GPUNames() // Qwen3-TTS runs on NVIDIA only: an AMD user is told so, not sent to install it
			}
			return nil, userErr("designing the voice failed: %v (it needs Qwen3-TTS, which runs on %s with free memory, installed in Integrations)", err, card)
		}
		clip := filepath.Join(os.TempDir(), "voice-design-"+a.Name+".wav")
		if err := os.WriteFile(clip, audio.EncodeWAV(pcm, rate), 0o644); err != nil {
			return nil, err
		}
		defer os.Remove(clip)
		res, err := s.Clone(ctx, CloneArgs{Action: "add", Name: a.Name, ClipPath: clip, Transcript: sample, Engine: target,
			RightsConfirmed: true, Rights: "designed from a description; no recorded person", Replace: a.Replace, Group: a.Group})
		if err != nil {
			return nil, err
		}
		if err := clones.SetDesigned(a.Name, desc); err != nil {
			return nil, err
		}
		out, _ := res.(map[string]any)
		if out == nil {
			out = map[string]any{"name": a.Name}
		}
		out["designed_from"] = desc
		if _, err := s.Clone(ctx, CloneArgs{Action: "preview", Name: a.Name}); err != nil {
			out["preview"] = "not played: " + err.Error()
		} else {
			out["preview"] = "playing; ask the user whether to keep it, and remove it if not"
		}
		return out, nil
	case "make_prompt":
		if err := need("name"); err != nil {
			return nil, err
		}
		p, err := clones.Get(a.Name)
		if err != nil {
			return nil, UserError{err.Error()}
		}
		if !KeepsPrompts(p.Engine) {
			return nil, userErr("%s keeps no voice prompts; %s does", p.Engine, strings.Join(promptEngines(), ", "))
		}
		if p.Text == "" {
			return nil, userErr("a voice prompt needs the transcript; call set_transcript first")
		}
		info, err := s.makePrompt(ctx, p)
		if err != nil {
			return nil, UserError{err.Error()}
		}
		return map[string]any{"name": p.Name, "prompt": info}, nil
	case "list":
		rows := [][]any{}
		for _, p := range clones.List() {
			rows = append(rows, []any{p.Name, core.ClonePresetName(p.Name), p.Group, p.Description, p.Engine, p.Duration, p.Text != "", p.Source, p.Designed})
		}
		return map[string]any{"columns": []string{"name", "preset", "group", "description", "engine", "seconds", "transcript", "source", "designed_from"}, "rows": rows,
			"groups": clones.Groups()}, nil
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
		if p := s.Speaker.Status().Preset; p == name || p == preset.LegacyClonePrefix+name {
			s.Speaker.SetPreset(s.Config.DefaultPreset)
		}
		s.Config.DeletePreset(name)
		return "removed " + a.Name, nil
	case "describe":
		// The user's description of a voice; it is also its preset's description.
		if err := need("name"); err != nil {
			return nil, err
		}
		if err := clones.SetDescription(a.Name, a.Description); err != nil {
			return nil, UserError{err.Error()}
		}
		p, err := clones.Get(a.Name)
		if err != nil {
			return nil, UserError{err.Error()}
		}
		s.Config.SetPreset(core.ClonePresetName(p.Name), core.ClonePreset(p))
		if p.Description == "" {
			return a.Name + " has no description now", nil
		}
		return "described " + a.Name, nil
	case "rename":
		if err := need("name"); err != nil {
			return nil, err
		}
		to := strings.TrimSpace(a.NewName)
		if to == "" {
			return nil, userErr("rename needs new_name")
		}
		if to == a.Name {
			return a.Name + " keeps its name", nil
		}
		if core.ClonePresetTaken(s.Config, to) {
			return nil, userErr("%q is already a voice preset; pick another name", to)
		}
		p, err := clones.Rename(a.Name, to)
		if err != nil {
			return nil, UserError{err.Error()}
		}
		old, name := core.ClonePresetName(a.Name), core.ClonePresetName(p.Name)
		s.Config.DeletePreset(old)
		s.Config.SetPreset(name, core.ClonePreset(p))
		if s.Speaker != nil && s.Speaker.Status().Preset == old {
			s.Speaker.SetPreset(name)
		}
		out := map[string]any{"name": p.Name, "preset": name}
		if s.RenamePreset != nil {
			if err := s.RenamePreset(old, name); err != nil {
				out["warning"] = "the characters and voices that used " + old + " were not updated: " + err.Error()
			}
		}
		return out, nil
	case "groups":
		return cloneGroups(), nil
	case "group":
		// With a name: file that voice in group (empty: in none). Without one:
		// a new, empty group.
		if a.Name == "" {
			g, err := clones.AddGroup(a.Group)
			if err != nil {
				return nil, UserError{err.Error()}
			}
			return "group " + g + " is there", nil
		}
		g, err := clones.SetGroup(a.Name, a.Group)
		if err != nil {
			return nil, UserError{err.Error()}
		}
		if g == "" {
			return a.Name + " is in no group", nil
		}
		return a.Name + " is in " + g, nil
	case "rename_group":
		if a.Group == "" || a.NewGroup == "" {
			return nil, userErr("rename_group needs group and new_group")
		}
		g, err := clones.RenameGroup(a.Group, a.NewGroup)
		if err != nil {
			return nil, UserError{err.Error()}
		}
		return "group " + a.Group + " is now " + g, nil
	case "remove_group":
		if a.Group == "" {
			return nil, userErr("remove_group needs group")
		}
		if err := clones.RemoveGroup(a.Group); err != nil {
			return nil, UserError{err.Error()}
		}
		return "group " + a.Group + " removed; its voices are in no group", nil
	}
	return nil, userErr("unknown action %q", a.Action)
}

// cloneGroups is the user's groups of voices in their order, each with its
// voices, then the voices in no group.
func cloneGroups() map[string]any {
	voices := clones.List()
	byGroup := map[string][]string{}
	var loose []string
	for _, p := range voices {
		if p.Group == "" {
			loose = append(loose, p.Name)
			continue
		}
		k := strings.ToLower(p.Group)
		byGroup[k] = append(byGroup[k], p.Name)
	}
	rows := [][]any{}
	for _, g := range clones.Groups() {
		v := byGroup[strings.ToLower(g)]
		if v == nil {
			v = []string{}
		}
		rows = append(rows, []any{g, v})
	}
	if loose == nil {
		loose = []string{}
	}
	return map[string]any{"columns": []string{"group", "voices"}, "rows": rows, "ungrouped": loose}
}

// design renders text in a voice made from desc by the design engine.
func (s *Service) design(ctx context.Context, desc, text, lang string) ([]float32, int, error) {
	e, err := s.Speaker.Engine(designEngine)
	if err != nil {
		return nil, 0, err
	}
	var pcm []float32
	rate := 0
	err = e.Synthesize(ctx, engine.Request{Text: text, Lang: lang, Speed: 1, Extra: map[string]any{"instruct": desc}}, func(c engine.Chunk) error {
		if rate == 0 {
			rate = c.SampleRate
		}
		pcm = append(pcm, c.PCM...)
		return nil
	})
	if err == nil && (len(pcm) == 0 || rate == 0) {
		err = errors.New("the design model returned no sound")
	}
	return pcm, rate, err
}

// KeepsPrompts reports whether an engine can save a voice prompt, so a
// cloned voice speaks without its recording (sidecar/backends.json).
func KeepsPrompts(engine string) bool {
	b, ok := sidecar.Find(engine)
	return ok && b.Prompts
}

func promptEngines() []string {
	var out []string
	for _, b := range sidecar.Backends() {
		if b.Prompts {
			out = append(out, b.Name)
		}
	}
	return out
}

// makePrompt has the voice's engine make its prompt from the original
// recording (the source copy, else ref.wav) and records it in the profile.
func (s *Service) makePrompt(ctx context.Context, p clones.Profile) (map[string]any, error) {
	e, err := s.Speaker.Engine(p.Engine)
	if err != nil {
		return nil, err
	}
	maker, ok := e.(interface {
		MakePrompt(ctx context.Context, ref, text, out string) (httpengine.PromptInfo, error)
	})
	if !ok {
		return nil, fmt.Errorf("engine %s cannot make a voice prompt", p.Engine)
	}
	ref := p.Ref
	if p.OriginalPath != "" {
		if _, err := os.Stat(p.OriginalPath); err == nil {
			ref = p.OriginalPath
		}
	}
	info, err := maker.MakePrompt(ctx, ref, p.Text, clones.PromptFile(p.Name))
	if err != nil {
		return nil, err
	}
	if err := clones.SetPrompt(p.Name, p.Engine, info.Model, info.Revision); err != nil {
		return nil, err
	}
	return map[string]any{"file": clones.PromptFile(p.Name), "model": info.Model, "revision": info.Revision, "from": filepath.Base(ref)}, nil
}

// saveSpeech renders text in the voice it would be said in, into a file
// nobody hears: not in the transcript, not stopped by mute.
func (s *Service) saveSpeech(ctx context.Context, a SpeakArgs) (any, error) {
	path := a.SaveTo
	if !filepath.IsAbs(path) {
		return nil, userErr("save_to must be an absolute path, got %q", path)
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext != ".wav" && ext != ".mp3" {
		return nil, userErr("save_to must end in .wav or .mp3")
	}
	preset, err := s.speakPreset(a)
	if err != nil {
		return nil, err
	}
	lines, _, err := s.script(a)
	if err != nil {
		return nil, err
	}
	t, err := s.Speaker.Speak(core.Request{
		Text: a.Text, Intent: core.IntentInfo, Lang: a.Lang,
		Preset: preset, SaveTo: path, Lines: lines, MaxChars: narrativeChars, Owner: a.PID, // a file is meant to hold it all
	})
	if err != nil {
		return nil, UserError{err.Error()}
	}
	select {
	case res := <-t.Done:
		if res.Err != nil {
			return nil, UserError{res.Err.Error()}
		}
		out := map[string]any{"file": path, "seconds": math.Round(res.Seconds*10) / 10, "engine": res.Engine}
		if t.Note != "" {
			out["voice_note"] = t.Note + ". Tell the user once."
		}
		return out, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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
