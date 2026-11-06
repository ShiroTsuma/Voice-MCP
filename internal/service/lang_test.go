package service

import (
	"context"
	"strings"
	"testing"

	"voice/internal/preset"
)

// A character's language overrides the general one; the agent hears it at the
// start and again only after it changed.
func TestSessionLanguage(t *testing.T) {
	on := true
	cfg := &preset.Config{DefaultLang: "en", Characters: []preset.Character{{Name: "Annie"}, {Name: "Rebekka", Lang: "pl"}}}
	cfg.Serve.Personas = &on
	s := &Service{Config: cfg, Agents: &Agents{Names: func() []string { return []string{"Annie", "Rebekka"} }}}
	call := func(a AgentArgs) AgentResult {
		res, _ := s.Agent(context.Background(), a)
		return res.(AgentResult)
	}
	start := call(AgentArgs{PID: 5, Event: "idle", Start: true})
	if start.SpeaksIn != "English" || !strings.Contains(start.LangNote, "in English") {
		t.Fatalf("start: %+v", start)
	}
	if again := call(AgentArgs{PID: 5, Event: "busy"}); again.LangNote != "" {
		t.Fatalf("told twice: %+v", again)
	}
	s.Agents.SetName(5, "Rebekka")
	next := call(AgentArgs{PID: 5, Event: "busy"})
	if next.SpeaksIn != "Polish" || !strings.Contains(next.LangNote, "in Polish") {
		t.Fatalf("Rebekka: %+v", next)
	}
	if got := s.speakLang(SpeakArgs{PID: 5}); got != "pl" {
		t.Fatalf("own voice: %s", got)
	}
	if got := s.speakLang(SpeakArgs{PID: 5, Voice: "Annie"}); got != "en" {
		t.Fatalf("as Annie: %s", got)
	}
	if got := s.speakLang(SpeakArgs{PID: 5, Preset: "narrator"}); got != "en" {
		t.Fatalf("a bare preset: %s", got)
	}
	cfg.DefaultLang = "auto"
	cfg.Languages = []string{"pl", "en"}
	if got := s.phraseLang(s.sessionLang(6)); got != "pl" {
		t.Fatalf("auto phrases: %s", got)
	}
	off := false
	cfg.Serve.Personas = &off
	if got := s.sessionLang(5); got != "auto" {
		t.Fatalf("characters off: %s", got)
	}
}
