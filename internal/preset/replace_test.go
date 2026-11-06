package preset

import "testing"

func TestReplacePresetsTakesTheFilesAndKeepsRuntimeOnes(t *testing.T) {
	cur, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	cur.SetPreset("voice-preview", Preset{Extends: "neutral"})
	cur.SetPreset("removed-voice", Preset{Extends: "neutral"})

	next, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	pitch := -1.5
	next.SetPreset("voice-anna", Preset{Extends: "calm-mature", Pitch: &pitch})

	cur.ReplacePresets(next, "voice-preview")
	if r, err := cur.Resolve("voice-anna"); err != nil || r.Pitch != -1.5 {
		t.Fatalf("voice-anna from the files: %+v, %v", r, err)
	}
	if !cur.HasPreset("voice-preview") {
		t.Error("the kept runtime preset is gone")
	}
	if cur.HasPreset("removed-voice") {
		t.Error("a preset the files no longer hold stayed")
	}
	// The two configs do not share the map afterwards.
	next.SetPreset("later", Preset{Extends: "neutral"})
	if cur.HasPreset("later") {
		t.Error("a preset added to the loaded config leaked into the live one")
	}
}
