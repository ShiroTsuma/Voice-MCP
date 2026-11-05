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
	for _, in := range []string{`{"scene":{"title":"One"}}`, `{"scene":"{\"title\":\"One\"}"}`} {
		var b SpeakArgs
		if err := json.Unmarshal([]byte(in), &b); err != nil || b.Scene == nil || b.Scene.Title != "One" {
			t.Fatalf("scene %s: %+v %v", in, b.Scene, err)
		}
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
	a := SpeakArgs{Voice: "brisk", Lines: []SpeakLine{{Text: "The door creaks open.", Voice: "Narrator"}, {Pause: 2}, {Text: "Who's there?", Voice: "clara", Pause: 1}, {Text: "Just me."}}}
	lines, _, _ = s.script(a)
	sc := s.scene(a, lines)
	if len(sc) != 3 || sc[0].Speaker != "Narrator" || sc[0].Pause != 2 || sc[0].Voice != "narrator" || sc[1].Speaker != "Clara" || sc[1].Voice != "calm-mature" || sc[1].Pause != 1 || sc[2].Speaker != "" || sc[2].Voice != "brisk" {
		t.Fatalf("scene: %+v", sc)
	}
	// A pause on a line, and a break alone, are kept for the speaker.
	lines, said, err = s.script(SpeakArgs{Lines: []SpeakLine{{Text: "One.", Pause: 1}, {Pause: 2}, {Text: "Two."}}})
	if err != nil || len(lines) != 3 || lines[0].Pause != 1 || lines[1].Pause != 2 || lines[1].Text != "" || said != "One. Two." {
		t.Fatalf("pauses: %+v %q %v", lines, said, err)
	}
	if _, _, err := s.script(SpeakArgs{Lines: []SpeakLine{{Pause: 2}}}); err == nil {
		t.Fatal("a script of breaks alone was taken")
	}
	if _, _, err := s.script(SpeakArgs{Lines: []SpeakLine{{Text: "Hi", Voice: "nobody"}}}); err == nil {
		t.Fatal("an unknown voice was taken")
	}
	if l, text, err := s.script(SpeakArgs{Text: "plain"}); err != nil || l != nil || text != "plain" {
		t.Fatalf("no lines: %v %q %v", l, text, err)
	}
}
