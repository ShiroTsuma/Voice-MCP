// Package hooks turns Claude Code hook input and Codex notify payloads into
// short spoken notifications: which project, and what happened, taken from
// the agent's last message when it can be read aloud.
package hooks

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"voice/internal/core"
	"voice/internal/preset"
)

// Kinds of events, matching the keys of [hooks.phrases].
const (
	Permission = "permission"
	Input      = "input"
	Done       = "done"
)

type Event struct {
	Kind    string
	Intent  string
	Client  string // claude | codex
	Project string // working directory name
	Summary string // the agent's last message, raw
	Thread  string // Codex: the thread (session) whose turn ended
	Tool    string // tool waiting for permission
}

// claudeInput covers the fields voice uses from Notification and Stop hooks.
type claudeInput struct {
	Event            string `json:"hook_event_name"`
	Message          string `json:"message"`
	NotificationType string `json:"notification_type"`
	TranscriptPath   string `json:"transcript_path"`
	StopHookActive   bool   `json:"stop_hook_active"`
	Cwd              string `json:"cwd"`
}

var reTool = regexp.MustCompile(`(?i)permission to use (\S+)`)

// FromClaude reads hook input from stdin. ok is false for events that should
// stay silent; reason says why, for -v diagnostics.
func FromClaude(r io.Reader, minTurn time.Duration) (ev Event, ok bool, reason string) {
	var in claudeInput
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return Event{}, false, "unreadable hook input: " + err.Error()
	}
	ev = Event{Client: "claude", Project: project(in.Cwd)}
	switch in.Event {
	case "Notification":
		msg := strings.ToLower(in.Message)
		switch {
		case in.NotificationType == "permission_prompt" || strings.Contains(msg, "permission"):
			ev.Kind, ev.Intent = Permission, core.IntentQuestion
			if m := reTool.FindStringSubmatch(in.Message); m != nil {
				ev.Tool = strings.TrimRight(m[1], ".,")
			}
			return ev, true, ""
		case in.NotificationType == "idle_prompt" || strings.Contains(msg, "waiting for your input"):
			ev.Kind, ev.Intent = Input, core.IntentQuestion
			return ev, true, ""
		}
		return Event{}, false, "notification not announced: " + in.Message
	case "Stop":
		if in.StopHookActive {
			return Event{}, false, "stop hook already active"
		}
		start, last, known := readTranscript(in.TranscriptPath)
		if known && time.Since(start) < minTurn {
			return Event{}, false, "short turn (" + time.Since(start).Round(time.Second).String() + ")"
		}
		ev.Kind, ev.Intent, ev.Summary = Done, core.IntentInfo, last
		return ev, true, ""
	}
	return Event{}, false, "event not announced: " + in.Event
}

// FromCodex reads the JSON payload Codex passes as the last argument of its
// notify command.
func FromCodex(payload string) (Event, bool, string) {
	var in struct {
		Type    string `json:"type"`
		Last    string `json:"last-assistant-message"`
		Cwd     string `json:"cwd"`
		CwdAlt  string `json:"working-directory"`
		Message string `json:"message"`
		Thread  string `json:"thread-id"`
	}
	if err := json.Unmarshal([]byte(payload), &in); err != nil {
		return Event{}, false, "unreadable notify payload: " + err.Error()
	}
	if in.Type != "agent-turn-complete" {
		return Event{}, false, "notification not announced: " + in.Type
	}
	cwd := in.Cwd
	if cwd == "" {
		cwd = in.CwdAlt
	}
	return Event{Kind: Done, Intent: core.IntentInfo, Client: "codex", Project: project(cwd), Summary: in.Last, Thread: in.Thread}, true, ""
}

func project(cwd string) string {
	if cwd == "" {
		return ""
	}
	return filepath.Base(filepath.Clean(cwd))
}

// readTranscript returns when the current turn started (the last genuine
// user message, not a tool result) and the last assistant text.
func readTranscript(path string) (start time.Time, lastText string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, "", false
	}
	defer f.Close()
	const tail = 2 << 20
	if st, err := f.Stat(); err == nil && st.Size() > tail {
		f.Seek(-tail, io.SeekEnd)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		isUser := bytes.Contains(line, []byte(`"type":"user"`))
		if !isUser && !bytes.Contains(line, []byte(`"type":"assistant"`)) {
			continue
		}
		var e struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch e.Type {
		case "user":
			if !e.Timestamp.IsZero() && !bytes.Contains(e.Message.Content, []byte(`"tool_result"`)) {
				start, lastText = e.Timestamp, ""
			}
		case "assistant":
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(e.Message.Content, &blocks) == nil {
				for _, b := range blocks {
					if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
						lastText = b.Text
					}
				}
			}
		}
	}
	return start, lastText, !start.IsZero()
}

// Phrase picks one configured phrase for the event at random, so repeated
// notifications do not sound canned.
func Phrase(h preset.Hooks, kind string) string {
	list := h.Phrases[kind]
	if len(list) == 0 {
		return ""
	}
	return list[rand.IntN(len(list))]
}

// maxSentence keeps each sentence of a spoken summary short.
const maxSentence = 160

// Compose builds what to say. With summaries on, a finished turn is told in
// the agent's own words (its last sentences, or its first), provided they are in
// lang; otherwise, and for other events, a configured phrase is used. The
// project name goes in front so several sessions can be told apart.
func Compose(h preset.Hooks, ev Event, lang string) (text string, cacheable bool) {
	message := ""
	if ev.Kind == Done && (h.Summary == nil || *h.Summary) {
		message = summarize(ev.Summary, lang, h.SummaryLength(), h.SummaryPart() == "start")
	}
	switch {
	case ev.Kind == Permission && ev.Tool == "AskUserQuestion":
		// Claude Code reports its question dialog as a permission prompt.
		message = "I have a question for you."
	case ev.Kind == Permission && ev.Tool != "" && (h.Summary == nil || *h.Summary):
		message = "I need your permission to use " + ev.Tool + "."
	}
	cacheable = message == ""
	if message == "" {
		message = Phrase(h, ev.Kind)
	}
	if message == "" {
		return "", false
	}
	if ev.Project != "" && (h.AnnounceProject == nil || *h.AnnounceProject) {
		message = ev.Project + ". " + message
	}
	return message, cacheable
}

func summarize(raw, lang string, sentences int, fromStart bool) string {
	text := core.Clean(raw)
	if text == "" {
		return ""
	}
	if lang != "" && lang != "auto" && core.DetectLang(text) != lang {
		return "" // e.g. a Polish reply read by an English voice
	}
	var parts []string
	for _, s := range core.Split(text, 0) {
		// A heading or a lone word ("Done.") is not a sentence worth saying.
		if s = strings.TrimSpace(s); utf8.RuneCountInString(s) >= 12 {
			parts = append(parts, core.Truncate(s, maxSentence))
		}
	}
	if len(parts) == 0 {
		return core.Truncate(text, maxSentence)
	}
	if n := min(sentences, len(parts)); fromStart {
		parts = parts[:n]
	} else {
		parts = parts[len(parts)-n:]
	}
	return strings.Join(parts, " ")
}

// CodexContinues reports whether Codex started another turn in thread right
// after the one that ended: a thread working toward a goal goes on by itself,
// and its notify after every turn is not the end of the work.
func CodexContinues(codexHome, thread string) bool {
	tail := codexRollout(codexHome, thread)
	done := strings.LastIndex(tail, `"type":"task_complete"`)
	return done >= 0 && strings.LastIndex(tail, `"type":"task_started"`) > done
}

// CodexHasGoal reports whether thread works toward a goal (Codex adds goal
// context to its turns): only such a thread may go on by itself.
func CodexHasGoal(codexHome, thread string) bool {
	return strings.Contains(codexRollout(codexHome, thread), `source=\"goal\"`)
}

// codexRollout is the end of thread's rollout in codexHome's sessions folder,
// "" when there is none.
func codexRollout(codexHome, thread string) string {
	if thread == "" || strings.ContainsAny(thread, `/\*?[`) {
		return ""
	}
	var newest string
	var newestAt time.Time
	for _, day := range []time.Time{time.Now(), time.Now().AddDate(0, 0, -1)} {
		dir := filepath.Join(codexHome, "sessions", day.Format("2006"), day.Format("01"), day.Format("02"))
		files, _ := filepath.Glob(filepath.Join(dir, "*"+thread+".jsonl"))
		for _, f := range files {
			if st, err := os.Stat(f); err == nil && st.ModTime().After(newestAt) {
				newest, newestAt = f, st.ModTime()
			}
		}
	}
	if newest == "" {
		return ""
	}
	return readTail(newest, 256<<10)
}
func readTail(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > n {
		f.Seek(st.Size()-n, io.SeekStart)
	}
	b, _ := io.ReadAll(f)
	return string(b)
}
