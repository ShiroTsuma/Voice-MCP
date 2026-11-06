package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"voice/internal/preset"
)

func TestFromClaudeNotification(t *testing.T) {
	cases := map[string]string{
		`{"hook_event_name":"Notification","message":"Claude needs your permission to use Bash"}`:        Permission,
		`{"hook_event_name":"Notification","message":"Claude is waiting for your input"}`:                Input,
		`{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"x"}`:       Permission,
		`{"hook_event_name":"Notification","notification_type":"idle_prompt","message":"anything else"}`: Input,
	}
	for in, want := range cases {
		ev, ok, reason := FromClaude(strings.NewReader(in), 0)
		if !ok || ev.Kind != want {
			t.Errorf("%s: got %+v ok=%v (%s), want %s", in, ev, ok, reason, want)
		}
	}
	if _, ok, _ := FromClaude(strings.NewReader(`{"hook_event_name":"Notification","message":"Something else"}`), 0); ok {
		t.Error("unknown notification announced")
	}
}

func transcript(t *testing.T, userAt time.Time) string {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	lines := []string{
		fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"role":"user","content":"do the thing"}}`, userAt.Format(time.RFC3339Nano)),
		fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`, time.Now().Format(time.RFC3339Nano)),
		`{"type":"assistant","message":{"content":"done"}}`,
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
	return path
}

func TestFromClaudeStopUsesTurnLength(t *testing.T) {
	stop := func(path string) string {
		return fmt.Sprintf(`{"hook_event_name":"Stop","stop_hook_active":false,"transcript_path":%q}`, path)
	}
	// Tool results must not count as the turn start.
	if _, ok, reason := FromClaude(strings.NewReader(stop(transcript(t, time.Now().Add(-5*time.Second)))), 30*time.Second); ok {
		t.Error("short turn announced")
	} else if !strings.Contains(reason, "short turn") {
		t.Errorf("reason %q", reason)
	}
	if ev, ok, _ := FromClaude(strings.NewReader(stop(transcript(t, time.Now().Add(-2*time.Minute)))), 30*time.Second); !ok || ev.Kind != Done {
		t.Error("long turn not announced")
	}
	if _, ok, _ := FromClaude(strings.NewReader(`{"hook_event_name":"Stop","stop_hook_active":true}`), 0); ok {
		t.Error("re-entrant stop announced")
	}
}

func TestFromCodex(t *testing.T) {
	if ev, ok, _ := FromCodex(`{"type":"agent-turn-complete","turn-id":"1","last-assistant-message":"x"}`); !ok || ev.Kind != Done {
		t.Error("turn complete not announced")
	}
	if _, ok, _ := FromCodex(`{"type":"other"}`); ok {
		t.Error("other type announced")
	}
}

func TestSummaryTakesTheChosenSentences(t *testing.T) {
	msg := "I fixed the parser bug in the tokenizer. All tests pass now. The release notes are updated too. Want me to push it?"
	cases := []struct {
		n     int
		start bool
		want  string
	}{
		{1, true, "I fixed the parser bug in the tokenizer."},
		{1, false, "Want me to push it?"},
		{2, false, "The release notes are updated too. Want me to push it?"},
		{3, false, "All tests pass now. The release notes are updated too. Want me to push it?"},
	}
	for _, c := range cases {
		if got := summarize(msg, "en", c.n, c.start); got != c.want {
			t.Errorf("%d start=%v: %q", c.n, c.start, got)
		}
	}
	if got := summarize("Done.", "en", 2, false); got != "Done." {
		t.Errorf("short message: %q", got)
	}
}

func TestCodexContinuesWhenTheGoalStartsAnotherTurn(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	dir := filepath.Join(home, "sessions", now.Format("2006"), now.Format("01"), now.Format("02"))
	os.MkdirAll(dir, 0o755)
	write := func(lines ...string) {
		os.WriteFile(filepath.Join(dir, "rollout-2026-11-04T00-26-15-thr1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}
	write(`{"payload":{"type":"task_started"}}`, `{"payload":{"type":"task_complete"}}`)
	if CodexContinues(home, "thr1") {
		t.Fatal("an ended thread goes on")
	}
	write(`{"payload":{"type":"task_started"}}`, `{"payload":{"type":"task_complete"}}`, `{"payload":{"type":"task_started"}}`)
	if !CodexContinues(home, "thr1") {
		t.Fatal("the goal's next turn not seen")
	}
	if CodexHasGoal(home, "thr1") {
		t.Fatal("a thread without a goal has one")
	}
	write(`{"payload":{"content":"<codex_internal_context source=\"goal\">Continue"}}`)
	if !CodexHasGoal(home, "thr1") {
		t.Fatal("the goal not seen")
	}
	if CodexContinues(home, "other") || CodexContinues(home, "../x") {
		t.Fatal("another thread matched")
	}
}

func TestQuestionDialogIsAnnouncedAsAQuestion(t *testing.T) {
	got, _ := Compose(preset.Hooks{}, Event{Kind: Permission, Tool: "AskUserQuestion"}, "en", "en")
	if got != "I have a question for you." {
		t.Fatalf("%q", got)
	}
	if got, _ := Compose(preset.Hooks{}, Event{Kind: Permission, Tool: "Bash"}, "en", "en"); got != "I need your permission to use Bash." {
		t.Fatalf("%q", got)
	}
}

// Phrases come in the voice's language; a summary in it is said, one in
// another gives way to a phrase; a language without phrases falls back to
// English; [hooks.phrases] replaces a kind in every language.
func TestPhrasesFollowTheVoicesLanguage(t *testing.T) {
	h := preset.Hooks{}
	if got, _ := Compose(h, Event{Kind: Permission, Tool: "Bash"}, "pl", "pl"); got != "Potrzebuję zgody na Bash." {
		t.Fatalf("pl permission: %q", got)
	}
	if got, _ := Compose(h, Event{Kind: Done, Summary: "Dodałam ostrzeżenie o języku w ustawieniach i wszystko działa."}, "pl", "pl"); !strings.Contains(got, "ostrzeżenie") {
		t.Fatalf("pl summary: %q", got)
	}
	got, cacheable := Compose(h, Event{Kind: Done, Summary: "I added the language warning to the settings and it works."}, "pl", "pl")
	if !cacheable || strings.Contains(got, "warning") || !slices.Contains(phrases["pl"][Done], got) {
		t.Fatalf("English summary for a Polish voice: %q", got)
	}
	if got, _ := Compose(h, Event{Kind: Input}, "de", "de"); !slices.Contains(phrases["en"][Input], got) {
		t.Fatalf("no German phrases: %q", got)
	}
	h.Phrases = map[string][]string{Input: {"Hej!"}}
	if got, _ := Compose(h, Event{Kind: Input}, "pl", "pl"); got != "Hej!" {
		t.Fatalf("config phrase: %q", got)
	}
}

// A file in the user's folder adds a language.
func TestUserPhrases(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "de.json"), []byte(`{"input": ["Du bist dran."]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	defer LoadPhrases("")
	if errs := LoadPhrases(dir); len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := Phrase(preset.Hooks{}, Input, "de", ""); got != "Du bist dran." {
		t.Fatalf("%q", got)
	}
	if got := Phrase(preset.Hooks{}, Done, "de", ""); !slices.Contains(phrases["en"][Done], got) {
		t.Fatalf("German without done: %q", got)
	}
}
