package service

import (
	"encoding/json"
	"testing"

	"voice/internal/preset"
)

func TestLinesAsAString(t *testing.T) {
	var a SpeakArgs
	if err := json.Unmarshal([]byte(`{"lines":"[{\"text\":\"Hi\",\"voice\":\"Anna\"}]"}`), &a); err != nil || len(a.Lines) != 1 || a.Lines[0].Voice != "Anna" {
		t.Fatalf("string: %+v %v", a.Lines, err)
	}
	if err := json.Unmarshal([]byte(`{"lines":[{"text":"Hi"}]}`), &a); err != nil || len(a.Lines) != 1 {
		t.Fatalf("list: %+v %v", a.Lines, err)
	}
}

func TestScriptLines(t *testing.T) {
	cfg, err := preset.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Characters = []preset.Character{{Name: "Narrator", Preset: "narrator"}, {Name: "Clara", Preset: "calm-mature"}}
	s := &Service{Config: cfg, Agents: &Agents{}}
	lines, said, err := s.script(SpeakArgs{Voice: "brisk", Lines: []SpeakLine{
		{Text: "The door creaks open.", Voice: "Narrator"},
		{Text: "Who's there?", Voice: "clara"},
		{Text: "  "},
		{Text: "Just me."},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[0].Preset != "narrator" || lines[1].Preset != "calm-mature" || lines[2].Preset != "brisk" {
		t.Fatalf("lines: %+v", lines)
	}
	if said != "Narrator: The door creaks open. clara: Who's there? Just me." {
		t.Fatalf("transcript: %q", said)
	}
	if _, _, err := s.script(SpeakArgs{Lines: []SpeakLine{{Text: "Hi", Voice: "nobody"}}}); err == nil {
		t.Fatal("an unknown voice was taken")
	}
	if l, text, err := s.script(SpeakArgs{Text: "plain"}); err != nil || l != nil || text != "plain" {
		t.Fatalf("no lines: %v %q %v", l, text, err)
	}
}
