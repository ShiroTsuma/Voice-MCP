package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Line is one entry of the transcript: something voice said, or something the
// user dictated, and who it was for.
type Line struct {
	At      time.Time `json:"at"`
	ID      string    `json:"id,omitempty"`      // said: the utterance, to match Status.Line
	Kind    string    `json:"kind"`              // said | dictated
	PID     uint32    `json:"pid,omitempty"`     // the session that spoke or was spoken to
	Name    string    `json:"name,omitempty"`    // its name then
	Client  string    `json:"client,omitempty"`  // claude, codex, ...
	Project string    `json:"project,omitempty"` // its folder
	Source  string    `json:"source,omitempty"`  // said: "hook" (an announcement) or "" (the agent itself)
	Text    string    `json:"text"`
	Outcome string    `json:"outcome,omitempty"` // dictated, message: where it went
	From    string    `json:"from,omitempty"`    // message: the session that sent it
	Scene   bool      `json:"scene,omitempty"`   // said: a script of lines ("Name: text ...")
	// Lines: a scene's lines one by one, for the reading window and its themes.
	Lines []SceneLine `json:"lines,omitempty"`
	// Setting: where and when the scene takes place, as the call gave it.
	Setting *SceneInfo `json:"setting,omitempty"`
}

// SceneLine is one line of a scene as the transcript keeps it.
type SceneLine struct {
	Speaker string  `json:"speaker,omitempty"` // the character who says it; empty: the session itself
	Voice   string  `json:"voice,omitempty"`   // the preset it is said in
	Text    string  `json:"text"`
	Pause   float64 `json:"pause,omitempty"` // seconds of silence after it
	// Shown, not spoken (see SpeakLine).
	Kind   string `json:"kind,omitempty"`
	Mood   string `json:"mood,omitempty"`
	Action string `json:"action,omitempty"`
	To     string `json:"to,omitempty"`
}

// transcriptSize is how many lines the transcript keeps.
const transcriptSize = 50

// Record adds a line to the transcript, filling in who pid is, and writes it
// to Logf.
func (a *Agents) Record(l Line) {
	if a == nil {
		return
	}
	l.At = time.Now()
	a.mu.Lock()
	if s := a.seen[l.PID]; s != nil && l.PID != 0 {
		l.Name, l.Client, l.Project = s.name, s.client, s.project
	}
	a.transcript = append(a.transcript, l)
	if n := len(a.transcript); n > transcriptSize {
		a.transcript = append([]Line(nil), a.transcript[n-transcriptSize:]...)
	}
	a.saveTranscriptLocked()
	logf := a.Logf
	a.mu.Unlock()
	if logf != nil {
		who := dirOr(l.Name, "voice")
		if l.Client != "" {
			who += " (" + l.Client + dirOr(", "+l.Project, "") + ")"
		}
		switch l.Kind {
		case "said":
			logf("said: %s%s: %s", who, map[bool]string{true: " [announcement]"}[l.Source == "hook"], clip(l.Text, 160))
		default:
			logf("dictated: %s: %s", clip(l.Text, 160), l.Outcome)
		}
	}
}

// Transcript is the transcript, oldest first.
func (a *Agents) Transcript() []Line {
	if a == nil {
		return []Line{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Line{}, a.transcript...)
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return fmt.Sprintf("%s…", string(r[:n]))
	}
	return s
}

// dictatedLocked reports whether prompt is what the user dictated to pid in
// the last few minutes; the caller holds a.mu.
func (a *Agents) dictatedLocked(pid uint32, prompt string) bool {
	p := strings.Join(strings.Fields(prompt), " ")
	if p == "" {
		return false
	}
	for i := len(a.transcript) - 1; i >= 0; i-- {
		l := a.transcript[i]
		if time.Since(l.At) > 5*time.Minute {
			break
		}
		if l.Kind == "dictated" && (l.PID == pid || l.PID == 0) && l.Text != "" && strings.Contains(p, strings.Join(strings.Fields(l.Text), " ")) {
			return true
		}
	}
	return false
}

// voiceReplies reports whether agents answer dictated messages aloud.
func (s *Service) voiceReplies() bool {
	return s.Config != nil && s.Config.Serve.VoiceReplies != nil && *s.Config.Serve.VoiceReplies
}

// TranscriptFor is the transcript for the voice_control tool: every line, or
// those of one session (session: its name now or then, a character's name,
// or "me" for the caller), the newest limit of them (default 20), oldest
// first, as columns and rows.
func (a *Agents) TranscriptFor(caller uint32, session string, limit int) map[string]any {
	if limit <= 0 {
		limit = 20
	}
	want := normalize(session)
	lines := a.Transcript()
	var picked []Line
	for _, l := range lines {
		switch {
		case want == "":
		case want == "me":
			if l.PID != caller || caller == 0 {
				continue
			}
		default:
			if normalize(l.Name) != want && normalize(a.Name(l.PID)) != want {
				continue
			}
		}
		picked = append(picked, l)
	}
	if len(picked) > limit {
		picked = picked[len(picked)-limit:]
	}
	rows := [][]string{}
	for _, l := range picked {
		kind := l.Kind
		if kind == "said" && l.Source == "hook" {
			kind = "announced"
		}
		rows = append(rows, []string{l.At.Format("15:04:05"), kind, l.Name, l.Project, l.Text, l.Outcome})
	}
	out := map[string]any{"columns": []string{"time", "kind", "session", "project", "text", "outcome"}, "rows": rows}
	if len(rows) == 0 && want != "" && want != "me" {
		out["note"] = "no lines for " + session + "; omit session for every session"
	}
	return out
}

// Count is a number an MCP client may send as a JSON number or as a string
// ("5"): clients with an older copy of the tool's schema send strings.
type Count int

func (c *Count) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*c = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("limit must be a whole number, not %s", b)
	}
	*c = Count(n)
	return nil
}
