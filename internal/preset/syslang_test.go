package preset

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDefaultLanguages(t *testing.T) {
	for in, want := range map[string][]string{
		"pl-PL": {"pl", "en"}, "uk_UA.UTF-8": {"uk", "en"}, "en-US": {"en"}, "": {"en"}, "C": {"en"},
	} {
		if got := DefaultLanguages(in); !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v", in, got, want)
		}
	}
}

// Unset languages take the system's and English; a list saved empty (none:
// any language) stays empty, and a chosen one stays as chosen.
func TestLanguagesDefault(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if want := DefaultLanguages(systemLanguage()); !slices.Equal(c.Languages, want) {
		t.Errorf("unset: %v, want %v", c.Languages, want)
	}
	if c, err = Load(write("none.toml", "languages = []\n")); err != nil || c.Languages == nil || len(c.Languages) != 0 {
		t.Errorf("saved empty: %#v, %v", c.Languages, err)
	}
	if c, err = Load(write("de.toml", "languages = [\"de\"]\nlanguage_notice = \"quality\"\n")); err != nil || !slices.Equal(c.Languages, []string{"de"}) || c.LanguageNotice != "quality" {
		t.Errorf("chosen: %v %q, %v", c.Languages, c.LanguageNotice, err)
	}
}
