package comets

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
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

func maxAlpha(px []byte) (m byte) {
	for i := 3; i < len(px); i += 4 {
		m = max(m, px[i])
	}
	return m
}

func TestSceneAppearsAndFadesAway(t *testing.T) {
	var sc scene
	if sc.step(Hidden, State{}, 0, 1.0/30) {
		t.Fatal("visible before anything was shown")
	}
	settle(&sc, Recording, State{Recording: true, Level: 0.5}, 0, 1)
	px := sc.render()
	if a := maxAlpha(px); a < 200 {
		t.Fatalf("brightest alpha while recording: %d", a)
	}
	if px[3] != 0 || px[len(px)-1] != 0 {
		t.Fatal("the corners are not transparent")
	}
	for i := 0; i < len(px); i += 4 {
		if a := px[i+3]; px[i] > a || px[i+1] > a || px[i+2] > a {
			t.Fatalf("pixel %d is not premultiplied: %v", i/4, px[i:i+4])
		}
	}
	settle(&sc, Hidden, State{}, 1, 2)
	if sc.step(Hidden, State{}, 3, 1.0/30) {
		t.Fatal("still visible after hiding")
	}
}

func TestQueuedShowsOneCometPerMessage(t *testing.T) {
	var sc scene
	settle(&sc, Queued, State{Queued: 3}, 0, 1.5)
	lit := 0
	for i := range sc.comets {
		if sc.comets[i].a > 0.9 {
			lit++
		}
	}
	if lit != 3 {
		t.Fatalf("comets lit: %d, want 3", lit)
	}
	settle(&sc, Queued, State{Queued: 5}, 1.5, 1.5)
	lit = 0
	for i := range sc.comets {
		if sc.comets[i].a > 0.9 {
			lit++
		}
	}
	if lit != 5 {
		t.Fatalf("comets lit after growing the queue: %d, want 5", lit)
	}
}

func TestTransitionsFlow(t *testing.T) {
	// No frame may change the picture abruptly: the mean absolute change of
	// alpha between consecutive frames stays small through every transition.
	var sc scene
	steps := []struct {
		k Kind
		s State
	}{
		{Recording, State{Recording: true, Level: 0.3}},
		{Recording, State{Recording: true, Level: 0.9}},
		{Transcribing, State{Transcribing: true}},
		{Queued, State{Queued: 4}},
		{Queued, State{Queued: 2}},
		{Done, State{}},
		{Hidden, State{}},
		{Problem, State{}},
		{Hidden, State{}},
	}
	prev := sc.render()
	tt := 0.0
	for _, st := range steps {
		for i := 0; i < 30; i++ {
			sc.step(st.k, st.s, tt, 1.0/30)
			tt += 1.0 / 30
			cur := sc.render()
			diff := 0
			for j := 3; j < len(cur); j += 4 {
				d := int(cur[j]) - int(prev[j])
				if d < 0 {
					d = -d
				}
				diff += d
			}
			if mean := float64(diff) / float64(Canvas*Canvas); mean > 8 {
				t.Fatalf("frame %d of %v changes alpha by %.1f on average", i, st.k, mean)
			}
			prev = cur
		}
	}
}

func BenchmarkFrame(b *testing.B) {
	var sc scene
	settle(&sc, Queued, State{Queued: 5}, 0, 1)
	s := State{Recording: true}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Level = float64(i%10) / 10
		sc.step(Recording, s, float64(i)/30, 1.0/30)
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
