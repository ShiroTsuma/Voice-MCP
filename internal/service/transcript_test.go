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
