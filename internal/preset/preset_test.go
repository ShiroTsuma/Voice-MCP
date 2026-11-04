package preset

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltinPresetsResolve(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range cfg.PresetNames() {
		if _, err := cfg.Resolve(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestUserOverlay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte(`
default_preset = "calm"
[engines.mytts]
type = "exec"
command = "mytts"
[presets.calm]
extends = "system"
speed = 0.85
voice = { a = 0.6, b = 0.4 }
lang.pl = { engine = "mytts", voice = "ref:clip.wav" }
`), 0o644)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := cfg.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "calm" || r.Engine != "sapi" || r.Speed != 0.85 || r.Chime != "soft" || r.Voice.Mix["a"] != 0.6 {
		t.Errorf("resolved %+v", r)
	}
	pl := r.For("pl")
	if pl.Engines[0] != "mytts" || pl.Voice.Ref != "clip.wav" || len(pl.Engines) != 2 || pl.Engines[1] != "espeak" {
		t.Errorf("pl choice %+v", pl)
	}
	if en := r.For("en"); en.Voice.Primary() != "a" || len(en.Engines) != 2 {
		t.Errorf("en choice %+v", en)
	}
}

func TestUnknownKeyRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("[presets.x]\nengnie = \"sapi\"\n"), 0o644)
	if _, err := Load(path); err == nil {
		t.Fatal("typo accepted")
	}
}

func TestEmptyCharactersOverride(t *testing.T) {
	if c, _ := Load(); len(c.Characters) == 0 {
		t.Fatal("no builtin characters")
	}
	path := filepath.Join(t.TempDir(), "settings.toml")
	os.WriteFile(path, []byte("characters = []\n"), 0o644)
	c, err := Load(path)
	if err != nil || len(c.Characters) != 0 {
		t.Fatalf("characters %v, %v", c.Characters, err)
	}
}

func TestBuiltinTraitsAndChoices(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Traits) < 10 || c.Traits[0] != "cheerful" {
		t.Fatalf("builtin traits: %v", c.Traits)
	}
	c.Characters = append(c.Characters, Character{Name: "Pirate", Traits: []string{"Pirate", "teasing"}})
	got := c.TraitChoices()
	if got[len(got)-1] != "Pirate" || len(got) != len(c.Traits)+1 {
		t.Fatalf("choices: %v", got)
	}
}

func TestLanguagesAreGeneral(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.toml")
	os.WriteFile(old, []byte("[listen]\nlanguages = [\"pl\", \"en\"]\n"), 0o644)
	cfg, err := Load(old)
	if err != nil || len(cfg.Languages) != 2 || cfg.Languages[0] != "pl" || len(cfg.Listen.Languages) != 2 {
		t.Fatalf("[listen] languages from earlier versions: %v %v %v", cfg.Languages, cfg.Listen.Languages, err)
	}
	top := filepath.Join(dir, "top.toml")
	os.WriteFile(top, []byte("languages = [\"de\"]\n"), 0o644)
	cfg, err = Load(old, top)
	if err != nil || len(cfg.Languages) != 1 || cfg.Languages[0] != "de" || cfg.Listen.Languages[0] != "de" {
		t.Fatalf("top-level languages win: %v %v %v", cfg.Languages, cfg.Listen.Languages, err)
	}
}
