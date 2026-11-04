package core

import (
	"reflect"
	"testing"
)

func TestClean(t *testing.T) {
	cases := map[string]string{
		"## Gotowe\n- zbudowane\n- **testy** przechodzą": "Gotowe. zbudowane. testy przechodzą.",
		"Run `go test` now": "Run go test now.",
		"See [the docs](https://x.y/z) or https://a.b/c": "See the docs or.",
		"Before\n```go\nfmt.Println(1)\n```\nAfter":      "Before. After.",
		"Done ✅ 🎉": "Done.",
		"`" + "averyveryveryverylongidentifier_name_here" + "` changed": "changed.",
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplit(t *testing.T) {
	got := Split("Skończyłam budowanie, np. pakietu voice. Wersja 1.2 działa! Co dalej? Tak.", 10)
	want := []string{"Skończyłam budowanie, np. pakietu voice.", "Wersja 1.2 działa!", "Co dalej? Tak."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Split = %q, want %q", got, want)
	}
	if got := Split(`He said "stop." Then left.`, 1); len(got) != 2 {
		t.Errorf("closing quote: %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("One two three. Four five six seven.", 20); got != "One two three." {
		t.Errorf("got %q", got)
	}
	if got := Truncate("alpha beta gamma delta", 13); got != "alpha beta…" {
		t.Errorf("got %q", got)
	}
}

func TestDetectLang(t *testing.T) {
	for text, want := range map[string]string{
		"Skończyłam, zerknij":             "pl",
		"Czy mam to zrobić teraz":         "pl",
		"I need your decision on this":    "en",
		"Build finished, tests are green": "en",
	} {
		if got := DetectLang(text); got != want {
			t.Errorf("DetectLang(%q) = %s, want %s", text, got, want)
		}
	}
}
