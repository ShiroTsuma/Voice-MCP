package main

import (
	"os"
	"path/filepath"
	"testing"

	"voice/internal/engine"
	"voice/internal/preset"
)

// The catalog lists each engine's voices by engine, ready only where its
// model is downloaded, with what was turned off left off; list_voices gives
// the ones on and ready.
func TestVoiceCatalog(t *testing.T) {
	old := engine.Dirs.Models
	engine.Dirs.Models = t.TempDir()
	t.Cleanup(func() { engine.Dirs.Models = old })
	cfg, err := preset.Load()
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(engine.Dirs.Models, cfg.Engines["kokoro"].String("model")), 0o755)
	os.MkdirAll(filepath.Join(engine.Dirs.Models, cfg.Engines["piper-darkman"].String("model")), 0o755)
	cfg.VoicesOff = []string{"kokoro/af_bella", "kokoro-gb/*"}

	cat := voiceCatalog(cfg, func() []engine.VoiceInfo {
		return []engine.VoiceInfo{{Name: "Microsoft Zira Desktop", Lang: "en-US", Gender: "Female"}}
	})
	byKey := map[string]catalogGroup{}
	for _, g := range cat {
		byKey[g.Key] = g
	}
	us, gb, st, piper, win := byKey["kokoro"], byKey["kokoro-gb"], byKey["supertonic"], byKey["piper"], byKey["sapi"]
	if !us.Ready || len(us.Voices) != 20 || us.Voices[0].ID != "kokoro/af_alloy" || us.Voices[0].Name != "Alloy" {
		t.Fatalf("Kokoro US: %+v", us)
	}
	if st.Ready || st.Needs == "" || len(st.Voices) != 10 || st.Voices[3].ID != "supertonic/3" || st.Voices[3].Name != "Voice 4" {
		t.Fatalf("Supertonic not downloaded: %+v", st)
	}
	if len(piper.Voices) != 1 || piper.Voices[0].ID != "piper-darkman/default" {
		t.Fatalf("Piper, only Darkman downloaded: %+v", piper)
	}
	if len(win.Voices) != 1 || win.Voices[0].Name != "Zira" || win.Voices[0].ID != "sapi/Microsoft Zira Desktop" {
		t.Fatalf("Windows: %+v", win)
	}
	for _, v := range gb.Voices {
		if v.On {
			t.Fatalf("kokoro-gb/* is off, %s is on", v.ID)
		}
	}
	rows := voicesOn(cat)["rows"].([][]string)
	for _, r := range rows {
		if r[0] == "kokoro/af_bella" || r[0] == "supertonic/0" || r[0] == "kokoro-gb/bf_emma" {
			t.Fatalf("list_voices offers %s", r[0])
		}
	}
	if len(rows) != 19+1+1 { // Kokoro US but Bella, Darkman, Zira
		t.Fatalf("list_voices: %d voices", len(rows))
	}
}
