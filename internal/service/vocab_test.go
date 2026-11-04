package service

import (
	"testing"

	"voice/internal/preset"
)

func TestVocabulary(t *testing.T) {
	v := NewVocabulary([]string{"Rebekka", "Zofia", "MCP-Tools", "README", "Anna", "rytmi -> README", "  ", "Rebekka"})
	for in, want := range map[string]string{
		"Rebecca asked Zofia to update the readme for MCP tools.": "Rebekka asked Zofia to update the README for MCP-Tools.",
		"Please check the Rytmi file.":                            "Please check the README file.",
		"rytmi":                                                   "README",
		"Rebecca, run the tests.":                                 "Rebekka, run the tests.",
		"and a cat":                                               "and a cat", // short words stay what they were
		"Anna, are you there?":                                    "Anna, are you there?",
		"the tools are ready":                                     "the tools are ready", // one word of a two-word entry is not the entry
		"":                                                        "",
		"rhythmic music":                                          "rhythmic music", // a replacement needs the whole word
	} {
		if got := v.Correct(in); got != want {
			t.Errorf("%q:\n got %q\nwant %q", in, got, want)
		}
	}
	if (*Vocabulary)(nil).Correct("x") != "x" {
		t.Fatal("nil vocabulary")
	}
}

func TestVocabularyFor(t *testing.T) {
	ag := &Agents{}
	ag.Seen(1, "claude", "MCP-Tools")
	ag.Seen(2, "agent", "recipes")
	cfg := &preset.Config{Characters: []preset.Character{{Name: "Rebekka"}}}
	cfg.Listen.Words = []string{"rytmi -> README"}
	v := VocabularyFor(cfg, ag)
	got := v.Correct("Rebecca, add a recipe to the Rytmi in MCP tools.")
	if want := "Rebekka, add a recipe to the README in MCP-Tools."; got != want {
		t.Fatalf("got %q", got)
	}
}
