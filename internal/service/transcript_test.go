package service

import (
	"fmt"
	"strings"
	"testing"
)

func TestTranscriptKeepsWhoSaidWhat(t *testing.T) {
	var logged []string
	a := &Agents{Logf: func(f string, args ...any) { logged = append(logged, fmt.Sprintf(f, args...)) }}
	a.Seen(5, "codex", "shop")
	name := a.Name(5)
	a.Record(Line{Kind: "said", PID: 5, Source: "hook", Text: "shop. Done."})
	a.Record(Line{Kind: "dictated", PID: 5, Text: "add the export button", Outcome: "queued"})
	got := a.Transcript()
	if len(got) != 2 || got[0].Name != name || got[0].Client != "codex" || got[1].Outcome != "queued" {
		t.Fatalf("transcript %+v", got)
	}
	if len(logged) != 2 || !strings.Contains(logged[0], "[announcement]") || !strings.Contains(logged[0], name+" (codex, shop)") {
		t.Fatalf("log %q", logged)
	}
	for i := 0; i < transcriptSize+10; i++ {
		a.Record(Line{Kind: "said", Text: fmt.Sprint(i)})
	}
	if got := a.Transcript(); len(got) != transcriptSize || got[len(got)-1].Text != fmt.Sprint(transcriptSize+9) {
		t.Fatalf("kept %d, last %q", len(got), got[len(got)-1].Text)
	}
}

func TestTranscriptForFilters(t *testing.T) {
	a := &Agents{}
	a.Seen(5, "codex", "shop")
	a.Seen(6, "claude", "site")
	five, six := a.Name(5), a.Name(6)
	a.Record(Line{Kind: "said", PID: 5, Source: "hook", Text: "shop. Done."})
	a.Record(Line{Kind: "dictated", PID: 6, Text: "fix the header", Outcome: "Delivered"})
	a.Record(Line{Kind: "said", PID: 6, Text: "Fixed."})
	rows := func(m map[string]any) [][]string { return m["rows"].([][]string) }
	if got := rows(a.TranscriptFor(5, "", 0)); len(got) != 3 || got[0][1] != "announced" || got[0][2] != five {
		t.Fatalf("all: %v", got)
	}
	if got := rows(a.TranscriptFor(5, strings.ToUpper(six), 0)); len(got) != 2 || got[1][4] != "Fixed." {
		t.Fatalf("by name: %v", got)
	}
	if got := rows(a.TranscriptFor(5, "me", 0)); len(got) != 1 || got[0][4] != "shop. Done." {
		t.Fatalf("me: %v", got)
	}
	if got := rows(a.TranscriptFor(5, "", 1)); len(got) != 1 || got[0][4] != "Fixed." {
		t.Fatalf("limit keeps the newest: %v", got)
	}
	if m := a.TranscriptFor(5, "Nobody", 0); len(rows(m)) != 0 || m["note"] == nil {
		t.Fatalf("unknown: %v", m)
	}
}
