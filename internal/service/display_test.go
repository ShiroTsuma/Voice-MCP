package service

import (
	"context"
	"path/filepath"
	"testing"

	"voice/internal/core"
	"voice/internal/preset"
)

// say's display text reaches the transcript; only text is spoken. Through
// Session as an agent of the HTTP API sends it, so both paths are covered
// (the MCP's voice_speak decodes into the same SpeakArgs).
func TestSayDisplay(t *testing.T) {
	cfg, err := preset.Load("")
	if err != nil {
		t.Fatal(err)
	}
	sp := core.NewSpeaker(cfg)
	sp.RecordTo(filepath.Join(t.TempDir(), "out.wav")) // nothing out loud
	t.Cleanup(sp.Close)
	s := &Service{Speaker: sp, Config: cfg, Agents: &Agents{}}
	ctx := context.Background()
	if _, err := s.Session(ctx, SessionArgs{Action: "register", Session: "rp", PID: 77, Name: "Mira"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, SessionArgs{Action: "say", Session: "rp", Text: "You're late again, darling.", Display: "*She leans against the doorframe.* \"You're late again, darling.\""}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, SessionArgs{Action: "say", Session: "rp", Text: "Plain line."}); err != nil {
		t.Fatal(err)
	}
	tr := s.Agents.Transcript()
	if len(tr) != 2 || tr[0].Text != "*She leans against the doorframe.* \"You're late again, darling.\"" || tr[0].Spoken != "You're late again, darling." {
		t.Fatalf("display line %+v", tr)
	}
	if tr[1].Spoken != "" || tr[1].Text != "Plain line." {
		t.Fatalf("plain line %+v", tr[1])
	}
}

// A reply's pieces with a display text keep what was shown and what was said
// apart; pieces without one keep the line as it was.
func TestTranscriptDisplay(t *testing.T) {
	a := &Agents{}
	a.Record(Line{Kind: "said", ID: "v1", Text: "Hello there."})
	a.Grow("v1", "*She smiles.* Good to see you.", "Good to see you.")
	a.Grow("v1", "Sit down.", "Sit down.")
	l := a.Transcript()[0]
	if l.Text != "Hello there. *She smiles.* Good to see you. Sit down." {
		t.Fatalf("shown %q", l.Text)
	}
	if l.Spoken != "Hello there. Good to see you. Sit down." {
		t.Fatalf("spoken %q", l.Spoken)
	}

	// Without a display text nothing extra is kept.
	a.Record(Line{Kind: "said", ID: "v2", Text: "Plain."})
	a.Grow("v2", "Still plain.", "Still plain.")
	if l := a.Transcript()[1]; l.Spoken != "" || l.Text != "Plain. Still plain." {
		t.Fatalf("plain line %+v", l)
	}
}
