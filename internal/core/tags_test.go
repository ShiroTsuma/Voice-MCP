package core

import (
	"reflect"
	"testing"
)

func TestTagsNeverReadAloud(t *testing.T) {
	// *laughs* in prose becomes the tag; other emphasis stays words.
	if got := Clean("Well *coughs* that is *really* odd."); got != "Well {cough} that is really odd." {
		t.Fatalf("clean: %q", got)
	}
	none := ParseExpressive(nil)
	u := newTagUse()
	if got, _ := forEngine(none, "Well {cough} that is odd {LAUGH}.", "", u); got != "Well that is odd ." {
		t.Fatalf("dropped: %q", got)
	}
	if !reflect.DeepEqual(keys(u.dropped), []string{"cough", "laugh"}) || len(u.sounds) != 0 {
		t.Fatalf("use: %+v", u)
	}
	if got := StripTags("Hi {laugh} there {pause:2}."); got != "Hi there ." {
		t.Fatalf("strip: %q", got)
	}
	if got := blankTags("a {sigh} b"); got != "a        b" {
		t.Fatalf("blank: %q", got)
	}
}

func TestTagsForAnEngine(t *testing.T) {
	x := ParseExpressive(map[string]any{
		"sounds":  map[string]any{"laugh": "[laugh]", "cough": "(coughs)"},
		"emotion": "labels",
		"moods":   map[string]any{"angry": "<|emotion:anger|>", "calm": map[string]any{"exaggeration": 0.3}},
	})
	u := newTagUse()
	got, extra := forEngine(x, "Ha {laugh} sorry {cough} {sigh}.", "angry", u)
	if got != "<|emotion:anger|> Ha [laugh] sorry (coughs) ." || extra != nil {
		t.Fatalf("labels: %q %v", got, extra)
	}
	if !reflect.DeepEqual(keys(u.sounds), []string{"cough", "laugh"}) || !reflect.DeepEqual(keys(u.dropped), []string{"sigh"}) || u.mood != "applied" {
		t.Fatalf("use: %+v", u)
	}
	if _, extra = forEngine(x, "Easy.", "calm", nil); extra["exaggeration"] != 0.3 {
		t.Fatalf("intensity: %v", extra)
	}
	u = newTagUse()
	if got, extra = forEngine(x, "Hm.", "sad", u); got != "Hm." || extra != nil || u.mood != "ignored" {
		t.Fatalf("unknown mood: %q %v %q", got, extra, u.mood)
	}
	ins := ParseExpressive(map[string]any{"emotion": "instruct", "instruct": "Speak in a {mood} way."})
	if _, extra = forEngine(ins, "Hi.", "teasing", nil); extra["instruct"] != "Speak in a teasing way." {
		t.Fatalf("instruct: %v", extra)
	}
	if !reflect.DeepEqual(x.SoundNames(), []string{"cough", "laugh"}) || !reflect.DeepEqual(ins.MoodNames(), []string{"any"}) || ParseExpressive(nil).Emotion != "none" {
		t.Fatalf("names: %v %v", x.SoundNames(), ins.MoodNames())
	}
}

func TestSplitPauses(t *testing.T) {
	pieces, pauses := splitPauses("One. {pause} Two {pause:1.5} {pause:0.5} three.")
	if !reflect.DeepEqual(pieces, []string{"One.", "Two", "three."}) || !reflect.DeepEqual(pauses, []float64{defaultPause, 2, 0}) {
		t.Fatalf("%q %v", pieces, pauses)
	}
	pieces, pauses = splitPauses("Plain {laugh} text.")
	if len(pieces) != 1 || pieces[0] != "Plain {laugh} text." || pauses[0] != 0 {
		t.Fatalf("no pause: %q %v", pieces, pauses)
	}
}
