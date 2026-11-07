package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"voice/internal/engine"
	"voice/internal/integrations"
	"voice/internal/models"
)

// One card holds a family of voices (Piper's Polish ones): each is its own
// download, and the card is on while any of them is there.
func TestModelsFamilyCard(t *testing.T) {
	old := engine.Dirs.Models
	engine.Dirs.Models = t.TempDir()
	t.Cleanup(func() { engine.Dirs.Models = old })
	f := integrationFile{Title: "Piper Polish", Uses: "model", Models: []string{"piper-pl-gosia", "piper-pl-darkman"}}

	st := modelsStatus(f)
	if st.State != integrations.Missing || len(st.Actions) != 2 || !st.Actions[0].Primary || st.Actions[0].ID != "download:piper-pl-gosia" {
		t.Fatalf("none downloaded: %+v", st)
	}
	m, _ := models.Find("piper-pl-darkman")
	os.MkdirAll(filepath.Join(engine.Dirs.Models, m.Dir), 0o755)
	st = modelsStatus(f)
	if st.State != integrations.On || !strings.Contains(st.Detail, "Darkman") || st.Actions[len(st.Actions)-1].ID != "remove:piper-pl-darkman" {
		t.Fatalf("Darkman downloaded: %+v", st)
	}
	h := builtinHandlers(nil)["model"]
	if err := h.do(t.Context(), f, "remove:piper-pl-darkman", func(string) {}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(engine.Dirs.Models, m.Dir)); err == nil {
		t.Fatal("Darkman was not removed")
	}
	if err := h.do(t.Context(), f, "download:kokoro", func(string) {}); err == nil {
		t.Fatal("a voice outside the family was taken")
	}
}
