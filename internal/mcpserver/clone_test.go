package mcpserver

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"voice/internal/audio"
	"voice/internal/clones"
)

func TestCloneFlow(t *testing.T) {
	clones.Dir = t.TempDir()
	cs, _ := newTestSession(t)

	pcm := make([]float32, 16000*6)
	for i := range pcm {
		pcm[i] = float32(0.3 * math.Sin(float64(i)/8))
	}
	clip := filepath.Join(t.TempDir(), "me.wav")
	os.WriteFile(clip, audio.EncodeWAV(pcm, 16000), 0o644)

	out, isErr := call(t, cs, "voice_clone", map[string]any{"action": "add", "name": "me", "clip_path": clip})
	if !isErr || !strings.Contains(out, "rights_confirmed") {
		t.Fatalf("clone without consent: %s", out)
	}
	out, isErr = call(t, cs, "voice_clone", map[string]any{"action": "add", "name": "me", "clip_path": clip, "rights_confirmed": true})
	if isErr || !strings.Contains(out, `"preset":"clone-me"`) {
		t.Fatalf("add: %s", out)
	}
	if out, _ = call(t, cs, "voice_control", map[string]any{"action": "list_presets"}); !strings.Contains(out, "clone-me") {
		t.Errorf("preset missing: %s", out)
	}
	if out, isErr = call(t, cs, "voice_control", map[string]any{"action": "set_preset", "preset": "clone-me"}); isErr {
		t.Errorf("set_preset: %s", out)
	}
	if out, _ = call(t, cs, "voice_clone", map[string]any{"action": "list"}); !strings.Contains(out, `"me"`) {
		t.Errorf("list: %s", out)
	}
	if out, isErr = call(t, cs, "voice_clone", map[string]any{"action": "remove", "name": "me"}); isErr {
		t.Fatalf("remove: %s", out)
	}
	if out, _ = call(t, cs, "voice_control", map[string]any{"action": "status"}); strings.Contains(out, "clone-me") {
		t.Errorf("active preset still points at removed clone: %s", out)
	}
}
