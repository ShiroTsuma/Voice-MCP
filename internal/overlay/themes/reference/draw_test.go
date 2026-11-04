package reference

import (
	"math"
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

// maxAlpha is the most opaque pixel of a premultiplied canvas.
func maxAlpha(px []byte) byte {
	var m byte
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
	settle(&sc, Recording, State{Recording: true, Level: 0.5}, 0, 1.5)
	px := sc.render()
	if a := maxAlpha(px); a < 200 {
		t.Fatalf("brightest alpha while recording: %d", a)
	}
	if px[3] != 0 || px[(Canvas*Canvas-1)*4+3] != 0 {
		t.Fatal("the corners are not transparent")
	}
	// The middle holds only the halo while recording: the ring is hollow.
	if a := px[(Canvas/2*Canvas+Canvas/2)*4+3]; a > 230 {
		t.Fatalf("centre alpha while recording: %d", a)
	}
	// Premultiplied: no channel above alpha.
	for i := 0; i < len(px); i += 4 {
		if px[i] > px[i+3] || px[i+1] > px[i+3] || px[i+2] > px[i+3] {
			t.Fatalf("pixel %d not premultiplied: %v", i/4, px[i:i+4])
		}
	}
	settle(&sc, Hidden, State{}, 1.5, 2.5)
	if sc.step(Hidden, State{}, 4, 1.0/30) {
		t.Fatal("still visible after hiding")
	}
}

func TestEveryStateIsFiniteAndDistinct(t *testing.T) {
	var sc scene
	kinds := []Kind{Recording, Transcribing, Queued, Done, Problem, Queued, Done, Hidden, Problem, Hidden}
	from := 0.0
	seen := map[Kind][]byte{}
	for _, k := range kinds {
		s := State{Recording: k == Recording, Level: 0.7, Transcribing: k == Transcribing, Queued: 3}
		settle(&sc, k, s, from, 1)
		from++
		for i := range sc.p {
			p := &sc.p[i]
			if math.IsNaN(p.ang+p.rad+p.bri) || math.IsInf(p.ang+p.rad+p.bri, 0) {
				t.Fatalf("%v: particle %d is %+v", k, i, *p)
			}
		}
		seen[k] = sc.render()
	}
	for a, pa := range seen {
		for b, pb := range seen {
			if a < b && string(pa) == string(pb) {
				t.Errorf("%v and %v render the same", a, b)
			}
		}
	}
}

func TestQueuedStarsAreCountable(t *testing.T) {
	// Each star should make a bright blob; count blobs round the ring by
	// sampling alpha along it.
	for n := 1; n <= 7; n++ {
		var sc scene
		settle(&sc, Queued, State{Queued: n}, 0, 2.5)
		px := sc.render()
		stars, gap := 0, 720
		for i := 0; i < 720; i++ {
			// Start midway between two stars so none straddles the scan's ends.
			a := sc.rot - math.Pi/2 + math.Pi/float64(n) + 2*math.Pi*float64(i)/720
			x := int(Canvas/2 + ringR*math.Cos(a))
			y := int(Canvas/2 + ringR*math.Sin(a))
			// A star is white in the middle: a bright blue channel.
			o := (y*Canvas + x) * 4
			if px[o+3] > 240 && px[o] > 230 {
				if gap > 24 { // dips inside a star are shorter than 12 degrees
					stars++
				}
				gap = 0
			} else {
				gap++
			}
		}
		if stars != n {
			t.Errorf("queued %d: counted %d stars", n, stars)
		}
	}
}

func BenchmarkFrame(b *testing.B) {
	var sc scene
	s := State{Recording: true, Level: 0.6}
	settle(&sc, Recording, s, 0, 1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t := 1 + float64(i)/30
		s.Level = 0.5 + 0.4*math.Sin(t*3)
		sc.step(Recording, s, t, 1.0/30)
		sc.render()
	}
}
