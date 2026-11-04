package waveform

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

func TestSceneAppearsAndFadesAway(t *testing.T) {
	var sc scene
	settle(&sc, Recording, State{Recording: true, Level: 0.5}, 0, 1)
	px := sc.render()
	// The ring is lit somewhere along its radius, right of the centre.
	lit := 0
	for x := Canvas / 2; x < Canvas; x++ {
		if px[(Canvas/2*Canvas+x)*4+3] > 200 {
			lit++
		}
	}
	if lit == 0 {
		t.Fatal("no opaque pixel on the ring while recording")
	}
	if px[3] != 0 {
		t.Fatal("the corner is not transparent")
	}
	for i := 0; i < Canvas*Canvas; i++ {
		a := px[i*4+3]
		for c := 0; c < 3; c++ {
			if px[i*4+c] > a {
				t.Fatalf("pixel %d: channel %d (%d) above alpha (%d): not premultiplied", i, c, px[i*4+c], a)
			}
		}
	}
	settle(&sc, Hidden, State{}, 1, 2)
	if sc.step(Hidden, State{}, 3, 1.0/30) {
		t.Fatal("still visible after hiding")
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
	// Merging: three pearls flying into the check mark after delivery.
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
