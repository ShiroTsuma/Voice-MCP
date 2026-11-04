package liquid

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestStatePriority(t *testing.T) {
	now := time.Now()
	cases := []struct {
		s    State
		want Kind
	}{
		{State{}, Hidden},
		{State{Queued: 2}, Queued},
		{State{Queued: 2, Flash: Done, FlashUntil: now.Add(time.Second)}, Done},
		{State{Queued: 2, Flash: Done, FlashUntil: now.Add(-time.Second)}, Queued},
		{State{Transcribing: true, Queued: 1}, Transcribing},
		{State{Recording: true, Transcribing: true}, Recording},
	}
	for _, c := range cases {
		if got := c.s.Show(now); got != c.want {
			t.Errorf("%+v: got %v, want %v", c.s, got, c.want)
		}
	}
}

// settle runs the scene for a while in one state, as the window would.
func settle(sc *scene, k Kind, s State, from, seconds float64) {
	for t := from; t < from+seconds; t += 1.0 / 30 {
		sc.step(k, s, t, 1.0/30)
	}
}

func TestSceneAppearsAndFadesAway(t *testing.T) {
	var sc scene
	settle(&sc, Recording, State{Recording: true, Level: 0.5}, 0, 1)
	px := sc.render()
	if a := px[(Canvas/2*Canvas+Canvas/2)*4+3]; a != 255 {
		t.Fatalf("centre alpha while recording: %d", a)
	}
	if px[3] != 0 {
		t.Fatal("the corner is not transparent")
	}
	settle(&sc, Hidden, State{}, 1, 2)
	if sc.step(Hidden, State{}, 3, 1.0/30) {
		t.Fatal("still visible after hiding")
	}
}

// TestFlows checks that no frame of a whole dictation jumps far from the one
// before it: every change of state is animated.
func TestFlows(t *testing.T) {
	frames := film(sequenceTL, 0, 0)
	worst, at := 0.0, 0
	for i := 1; i < len(frames); i++ {
		var sum float64
		a, b := frames[i-1], frames[i]
		for k := 3; k < len(a); k += 4 {
			sum += math.Abs(float64(a[k]) - float64(b[k]))
		}
		if m := sum / float64(Canvas*Canvas); m > worst {
			worst, at = m, i
		}
	}
	t.Logf("worst mean alpha change between frames: %.2f/255 at frame %d", worst, at)
	// A cut from one look to another would change it by 50 or more.
	if worst > 12 {
		t.Fatalf("a frame jumps: mean alpha change %.2f/255", worst)
	}
	if last := frames[len(frames)-1]; slices.ContainsFunc(last, func(v byte) bool { return v != 0 }) {
		t.Fatal("the last frame is not empty")
	}
}

// BenchmarkFrame is one window frame (step and render), going through every
// state in turn.
func BenchmarkFrame(b *testing.B) {
	var sc scene
	seq := []struct {
		k Kind
		s State
	}{
		{Recording, State{Recording: true, Level: 0.7}},
		{Transcribing, State{Transcribing: true}},
		{Queued, State{Queued: 7}},
		{Done, State{}},
		{Problem, State{}},
	}
	for i := 0; i < b.N; i++ {
		t := float64(i) / 30
		st := seq[i/45%len(seq)]
		sc.step(st.k, st.s, t, 1.0/30)
		sc.render()
	}
}

// VOICE_OVERLAY_PNG=dir writes frames of every state, to look at.
func TestWriteFrames(t *testing.T) {
	dir := os.Getenv("VOICE_OVERLAY_PNG")
	if dir == "" {
		t.Skip("set VOICE_OVERLAY_PNG=dir")
	}
	states := []struct {
		name string
		k    Kind
		s    State
	}{
		{"recording-quiet", Recording, State{Recording: true, Level: 0.1}},
		{"recording-loud", Recording, State{Recording: true, Level: 0.9}},
		{"transcribing", Transcribing, State{Transcribing: true}},
		{"queued-3", Queued, State{Queued: 3}},
		{"done", Done, State{}},
		{"problem", Problem, State{}},
	}
	for _, st := range states {
		var sc scene
		settle(&sc, st.k, st.s, 0, 1.3)
		write(t, filepath.Join(dir, st.name+".png"), sc.render())
	}
	// Merging: three satellites flowing into the middle after delivery.
	var sc scene
	settle(&sc, Queued, State{Queued: 3}, 0, 1.3)
	for i, d := range []float64{0.08, 0.16, 0.3} {
		settle(&sc, Done, State{}, 1.3, d-map[int]float64{0: 0, 1: 0.08, 2: 0.16}[i])
		write(t, filepath.Join(dir, "merge-"+string(rune('1'+i))+".png"), sc.render())
	}
}

func write(t *testing.T, path string, buf []byte) {
	img := image.NewNRGBA(image.Rect(0, 0, Canvas, Canvas))
	for i := 0; i < Canvas*Canvas; i++ {
		a := buf[i*4+3]
		un := func(v byte) uint8 {
			if a == 0 {
				return 0
			}
			return uint8(min(255, int(v)*255/int(a)))
		}
		img.Set(i%Canvas, i/Canvas, color.NRGBA{un(buf[i*4+2]), un(buf[i*4+1]), un(buf[i*4]), a})
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	png.Encode(f, img)
	f.Close()
}
