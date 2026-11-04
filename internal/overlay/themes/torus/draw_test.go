package torus

import (
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
	if sc.step(Hidden, State{}, 0, 1.0/30) {
		t.Fatal("visible before anything was shown")
	}
	settle(&sc, Recording, State{Recording: true, Level: 0.5}, 0, 1)
	px := sc.render()
	if a := px[(Canvas/2*Canvas+Canvas/2)*4+3]; a < 180 {
		t.Fatalf("the halo behind the ring is too thin: alpha %d", a)
	}
	bright := 0
	for i := 0; i < len(px); i += 4 {
		if px[i+3] > 200 && px[i] > 200 {
			bright++
		}
	}
	if bright < 60 {
		t.Fatalf("only %d bright pixels while recording", bright)
	}
	for _, c := range []int{0, Canvas - 1, Canvas * (Canvas - 1), Canvas*Canvas - 1} {
		if px[c*4+3] != 0 {
			t.Fatal("a corner is not transparent")
		}
	}
	settle(&sc, Hidden, State{}, 1, 2)
	if sc.step(Hidden, State{}, 3, 1.0/30) {
		t.Fatal("still visible after hiding")
	}
	if px := sc.render(); px[(Canvas/2*Canvas+Canvas/2)*4+3] != 0 {
		t.Fatal("drawn while hidden")
	}
}

// Premultiplied pixels never have a colour brighter than their alpha.
func TestPremultiplied(t *testing.T) {
	var sc scene
	now := 0.0
	for _, st := range []struct {
		k Kind
		s State
	}{
		{Recording, State{Recording: true, Level: 1}},
		{Transcribing, State{Transcribing: true}},
		{Queued, State{Queued: 7}},
		{Done, State{}},
		{Problem, State{}},
	} {
		settle(&sc, st.k, st.s, now, 0.7)
		now += 0.7
		px := sc.render()
		for i := 0; i < len(px); i += 4 {
			if px[i] > px[i+3] || px[i+1] > px[i+3] || px[i+2] > px[i+3] {
				t.Fatalf("%v: pixel %d is not premultiplied: %v", st.k, i/4, px[i:i+4])
			}
		}
	}
}

func BenchmarkFrame(b *testing.B) {
	var sc scene
	settle(&sc, Recording, State{Recording: true, Level: 0.5}, 0, 1)
	t := 1.0
	for i := 0; b.Loop(); i++ {
		t += 1.0 / 30
		lv := 0.5 + 0.5*float64(i%7)/7
		sc.step(Recording, State{Recording: true, Level: lv}, t, 1.0/30)
		sc.render()
	}
}
