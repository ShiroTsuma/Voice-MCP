package aurora

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
	settle(&sc, Recording, State{Recording: true, Level: 0.5}, 0, 1.5)
	px := sc.render()
	if len(px) != Canvas*Canvas*4 {
		t.Fatalf("%d bytes", len(px))
	}
	// The ring is lit, the middle is the dark halo, the corners are clear.
	var ring int
	for y := 0; y < Canvas; y++ {
		for x := 0; x < Canvas; x++ {
			i := (y*Canvas + x) * 4
			if px[i] > px[i+3] || px[i+1] > px[i+3] || px[i+2] > px[i+3] {
				t.Fatalf("%d,%d is not premultiplied", x, y)
			}
			if px[i+3] > 200 && px[i] > 150 {
				ring++
			}
		}
	}
	if ring < 40 {
		t.Fatalf("only %d bright pixels while recording", ring)
	}
	if c := px[(Canvas/2*Canvas+Canvas/2)*4:]; c[3] < 120 || c[0] > 120 {
		t.Fatalf("centre is not a dark halo: %v", c[:4])
	}
	for _, i := range []int{0, Canvas - 1, Canvas * (Canvas - 1), Canvas*Canvas - 1} {
		if px[i*4+3] != 0 {
			t.Fatal("a corner is not transparent")
		}
	}
	settle(&sc, Hidden, State{}, 1.5, 2)
	if sc.step(Hidden, State{}, 3.5, 1.0/30) {
		t.Fatal("still visible after hiding")
	}
	for _, b := range sc.render() {
		if b != 0 {
			t.Fatal("hidden but drawn")
		}
	}
}

// Every state looks different: count lit pixels in a few regions.
func TestStatesDiffer(t *testing.T) {
	states := []struct {
		k Kind
		s State
	}{
		{Recording, State{Recording: true, Level: 0.5}},
		{Transcribing, State{Transcribing: true}},
		{Queued, State{Queued: 3}},
		{Done, State{}},
		{Problem, State{}},
	}
	seen := map[[4]int]Kind{}
	for _, st := range states {
		var sc scene
		settle(&sc, st.k, st.s, 0, 1.2)
		px := sc.render()
		var q [4]int // lit pixels in the centre, and in thirds of the ring
		for y := 0; y < Canvas; y++ {
			for x := 0; x < Canvas; x++ {
				i := (y*Canvas + x) * 4
				if int(px[i])+int(px[i+1])+int(px[i+2]) < 300 {
					continue
				}
				dx, dy := float64(x)-center, float64(y)-center
				if dx*dx+dy*dy < 20*20 {
					q[0]++
				} else {
					q[1+int((dy+center)*3/Canvas)]++
				}
			}
		}
		for j := range q {
			q[j] /= 25
		}
		if k, ok := seen[q]; ok {
			t.Errorf("%v looks like %v: %v", st.k, k, q)
		}
		seen[q] = st.k
	}
}

func BenchmarkFrame(b *testing.B) {
	var sc scene
	settle(&sc, Recording, State{Recording: true, Level: 0.6}, 0, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t := 1 + float64(i)/30
		lv := 0.5 + 0.5*float64(i%9)/9
		sc.step(Recording, State{Recording: true, Level: lv}, t, 1.0/30)
		sc.render()
	}
}
