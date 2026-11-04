package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"voice/internal/preset"
)

// The lists voice ships: they parse, and they fix what recognition writes
// without touching ordinary words.
func TestShippedWordLists(t *testing.T) {
	shipped := filepath.Join("..", "..", "wordlists")
	if errs := LoadWordLists(shipped, t.TempDir()); len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(WordLists()) < 2 {
		t.Fatalf("lists: %+v", WordLists())
	}
	cfg := &preset.Config{Languages: []string{"pl", "en"}}
	v := VocabularyFor(cfg, &Agents{})
	for in, want := range map[string]string{
		"Popraw rytmi w repo":                         "Popraw README w repo",
		"Dodaj pole do dżejsona i zrób komit":         "Dodaj pole do JSON-a i zrób commit",
		"Wrzuć to na githaba i otwórz pul rekłest":    "Wrzuć to na GitHuba i otwórz pull request",
		"Zmerdżuj brancza i zrób deploj":              "Zmerge'uj brancha i zrób deploy",
		"Please update the read me on git hub":        "Please update the README on GitHub",
		"Put it behind engine x on local host":        "Put it behind nginx on localhost",
		"I built it yesterday and it took a while":    "I built it yesterday and it took a while", // ordinary words stay
		"Komitet zbierze się w piątek":                "Komitet zbierze się w piątek",
		"She was taken to the station by a comet fan": "She was taken to the station by a comet fan",
	} {
		if got := v.Correct(in); got != want {
			t.Errorf("%q:\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestWordListsOnAndImport(t *testing.T) {
	shipped, imported := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(shipped, "a.json"), []byte(`{"title":"A","languages":["pl"],"default":true,"words":{"README":["rytmi"]}}`), 0o644)
	os.WriteFile(filepath.Join(shipped, "b.json"), []byte(`{"title":"B","languages":["de"],"default":true,"words":{"Docker":["doka"]}}`), 0o644)
	LoadWordLists(shipped, imported)
	ids := func(ws []WordList) string {
		var out []string
		for _, w := range ws {
			out = append(out, w.ID)
		}
		return strings.Join(out, ",")
	}
	if got := ids(ListsOn(nil, []string{"pl"})); got != "a" {
		t.Fatalf("default for Polish: %s", got)
	}
	if got := ids(ListsOn([]string{"b"}, []string{"pl"})); got != "b" {
		t.Fatalf("chosen: %s", got)
	}
	if got := ids(ListsOn([]string{}, []string{"pl"})); got != "" {
		t.Fatalf("all turned off: %s", got)
	}
	if _, err := ImportWordList([]byte(`{"words":{"Kubernetes":["kubernetis"]}}`), "Mine.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(imported, "mine.json")); err != nil {
		t.Fatal("imported list not stored")
	}
	if _, err := ImportWordList([]byte(`{"words":{}}`), "empty.json"); err == nil {
		t.Fatal("an empty list was imported")
	}
	if err := RemoveWordList("a"); err == nil {
		t.Fatal("removed a list voice ships")
	}
	if err := RemoveWordList("mine"); err != nil || strings.Contains(ids(WordLists()), "mine") {
		t.Fatalf("remove: %v %s", err, ids(WordLists()))
	}
}
