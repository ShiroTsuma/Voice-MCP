package waveform

import (
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A segment of a scripted animation: kind and state for dur seconds. A
// state function gets the time into the segment; dur <= 0 means "until the
// scene reports nothing visible".
type segment struct {
	dur   float64
	kind  Kind
	state func(t float64) State
}

func fixed(s State) func(float64) State { return func(float64) State { return s } }

// speech is a speech-like microphone envelope: syllables inside words, with
// pauses, swelling from quiet to loud and back over dur seconds.
func speech(dur float64) func(float64) State {
	return func(t float64) State {
		arc := math.Sin(math.Pi * t / dur)
		words := smooth(0.25, 0.55, 0.5+0.5*math.Sin(2*math.Pi*0.55*t+0.4))
		syl := 0.5 + 0.5*math.Sin(2*math.Pi*3.6*t+0.9*math.Sin(2*math.Pi*0.7*t))
		syl = math.Pow(syl, 1.6)
		accent := 0.65 + 0.35*math.Sin(2*math.Pi*1.3*t+1)
		l := arc * words * (0.12 + 0.88*syl) * accent
		return State{Recording: true, Level: clamp(0.04 + l)}
	}
}

// play runs the script and hands every rendered frame to fn.
func play(script []segment, fn func(px []byte)) {
	var sc scene
	const dt = 1.0 / 30
	t := 0.0
	for _, seg := range script {
		for st := 0.0; seg.dur <= 0 || st < seg.dur; st += dt {
			visible := sc.step(seg.kind, seg.state(st), t, dt)
			t += dt
			if !visible {
				if seg.dur <= 0 {
					break
				}
				fn(nil)
				continue
			}
			fn(sc.render())
		}
	}
}

// frameImage composites a canvas over bg, upscaled 2x.
func frameImage(px []byte, bg color.RGBA) *image.RGBA {
	const S = 2
	img := image.NewRGBA(image.Rect(0, 0, Canvas*S, Canvas*S))
	for y := 0; y < Canvas*S; y++ {
		for x := 0; x < Canvas*S; x++ {
			c := bg
			if px != nil {
				i := ((y/S)*Canvas + x/S) * 4
				a := int(px[i+3])
				blend := func(src byte, dst uint8) uint8 {
					return uint8(int(src) + int(dst)*(255-a)/255)
				}
				c = color.RGBA{blend(px[i+2], bg.R), blend(px[i+1], bg.G), blend(px[i], bg.B), 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// quantizer builds a median-cut palette from sample frames and maps pixels
// to it through a 6-bit-per-channel cache.
type quantizer struct {
	pal   color.Palette
	cache []uint8 // 0 = unknown, else index+1
}

func newQuantizer(frames []*image.RGBA, n int) *quantizer {
	var samples [][3]uint8
	for fi, f := range frames {
		if fi%2 != 0 {
			continue
		}
		p := f.Pix
		for i := 0; i < len(p); i += 8 {
			samples = append(samples, [3]uint8{p[i], p[i+1], p[i+2]})
		}
	}
	boxes := [][][3]uint8{samples}
	for len(boxes) < n {
		// Split the box with the widest channel range.
		best, bestRange, bestCh := -1, -1, 0
		for bi, b := range boxes {
			if len(b) < 2 {
				continue
			}
			var lo, hi [3]int
			for c := range lo {
				lo[c], hi[c] = 255, 0
			}
			for _, s := range b {
				for c := range s {
					lo[c], hi[c] = min(lo[c], int(s[c])), max(hi[c], int(s[c]))
				}
			}
			for c := range lo {
				if r := hi[c] - lo[c]; r > bestRange {
					best, bestRange, bestCh = bi, r, c
				}
			}
		}
		if best < 0 || bestRange == 0 {
			break
		}
		b := boxes[best]
		slices.SortFunc(b, func(x, y [3]uint8) int { return int(x[bestCh]) - int(y[bestCh]) })
		m := len(b) / 2
		boxes[best] = b[:m]
		boxes = append(boxes, b[m:])
	}
	q := &quantizer{cache: make([]uint8, 1<<18)}
	for _, b := range boxes {
		var sum [3]int
		for _, s := range b {
			for c := range s {
				sum[c] += int(s[c])
			}
		}
		q.pal = append(q.pal, color.RGBA{uint8(sum[0] / len(b)), uint8(sum[1] / len(b)), uint8(sum[2] / len(b)), 255})
	}
	return q
}

func (q *quantizer) index(r, g, b uint8) uint8 {
	key := int(r>>2)<<12 | int(g>>2)<<6 | int(b>>2)
	if v := q.cache[key]; v != 0 {
		return v - 1
	}
	best, bestD := 0, math.MaxInt
	for i, c := range q.pal {
		cr, cg, cb, _ := c.RGBA()
		dr, dg, db := int(cr>>8)-int(r), int(cg>>8)-int(g), int(cb>>8)-int(b)
		if d := dr*dr + dg*dg + db*db; d < bestD {
			best, bestD = i, d
		}
	}
	q.cache[key] = uint8(best + 1)
	return uint8(best)
}

func (q *quantizer) frame(f *image.RGBA) *image.Paletted {
	p := image.NewPaletted(f.Bounds(), q.pal)
	for i := 0; i < len(p.Pix); i++ {
		p.Pix[i] = q.index(f.Pix[i*4], f.Pix[i*4+1], f.Pix[i*4+2])
	}
	return p
}

func writeGIF(t *testing.T, path string, script []segment, bg color.RGBA) {
	var frames []*image.RGBA
	play(script, func(px []byte) { frames = append(frames, frameImage(px, bg)) })
	q := newQuantizer(frames, 255)
	g := &gif.GIF{LoopCount: 0}
	for i, f := range frames {
		g.Image = append(g.Image, q.frame(f))
		delay := 3 // 30 fps: 3, 3, 4 hundredths
		if i%3 == 2 {
			delay = 4
		}
		g.Delay = append(g.Delay, delay)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err := gif.EncodeAll(out, g); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %d frames", filepath.Base(path), len(frames))
}

var (
	dark  = color.RGBA{0x1e, 0x1e, 0x22, 255}
	light = color.RGBA{0xf3, 0xf3, 0xf3, 255}
)

func sequence() []segment {
	return []segment{
		{0.3, Hidden, fixed(State{})},
		{4, Recording, speech(4)},
		{2, Transcribing, fixed(State{Transcribing: true})},
		{2, Queued, fixed(State{Queued: 2})},
		{1.6, Done, fixed(State{})},
		{0, Hidden, fixed(State{})},
		{0.4, Hidden, fixed(State{})},
	}
}

// VOICE_OVERLAY_GIF=dir writes animated GIFs of every state, and a still
// contact sheet, to look at.
func TestWriteGIFs(t *testing.T) {
	dir := os.Getenv("VOICE_OVERLAY_GIF")
	if dir == "" {
		t.Skip("set VOICE_OVERLAY_GIF=dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gifs := []struct {
		name   string
		script []segment
		bg     color.RGBA
	}{
		{"recording", []segment{{6, Recording, speech(6)}}, dark},
		{"transcribing", []segment{{3, Transcribing, fixed(State{Transcribing: true})}}, dark},
		{"queued", []segment{{4, Queued, fixed(State{Queued: 3})}}, dark},
		{"deliver", []segment{{1.5, Queued, fixed(State{Queued: 3})}, {1.6, Done, fixed(State{})}, {0, Hidden, fixed(State{})}}, dark},
		{"problem", []segment{{2.5, Problem, fixed(State{})}}, dark},
		{"sequence", sequence(), dark},
		{"sequence-light", sequence(), light},
	}
	for _, g := range gifs {
		writeGIF(t, filepath.Join(dir, g.name+".gif"), g.script, g.bg)
	}
}

// VOICE_OVERLAY_GIF=dir also writes still contact sheets: twelve
// representative frames on dark and light, and a strip of consecutive
// recording frames to check the motion.
func TestWriteStills(t *testing.T) {
	dir := os.Getenv("VOICE_OVERLAY_GIF")
	if dir == "" {
		t.Skip("set VOICE_OVERLAY_GIF=dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStill(t, filepath.Join(dir, "still.png"), dark)
	writeStill(t, filepath.Join(dir, "still-light.png"), light)
	strip := func(name string, script []segment, from, count int) {
		var frames []*image.RGBA
		n := 0
		play(script, func(px []byte) {
			if n >= from && n < from+count {
				frames = append(frames, frameImage(px, dark))
			}
			n++
		})
		writeSheet(t, filepath.Join(dir, name+".png"), frames, 4)
	}
	strip("strip-recording", []segment{{3, Recording, speech(3)}}, 50, 12)
	strip("strip-deliver", []segment{{1.5, Queued, fixed(State{Queued: 3})}, {1.6, Done, fixed(State{})}, {0, Hidden, fixed(State{})}}, 44, 12)
	strip("strip-dissolve", []segment{{1.5, Queued, fixed(State{Queued: 3})}, {1.6, Done, fixed(State{})}, {0, Hidden, fixed(State{})}}, 92, 12)
	strip("strip-appear", []segment{{1, Recording, fixed(State{Recording: true, Level: 0.3})}}, 0, 12)
}

func writeSheet(t *testing.T, path string, frames []*image.RGBA, cols int) {
	const S = Canvas * 2
	rows := (len(frames) + cols - 1) / cols
	sheet := image.NewRGBA(image.Rect(0, 0, cols*S, rows*S))
	for i, img := range frames {
		ox, oy := (i%cols)*S, (i/cols)*S
		for y := 0; y < S; y++ {
			for x := 0; x < S; x++ {
				sheet.Set(ox+x, oy+y, img.RGBAAt(x, y))
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, sheet); err != nil {
		t.Fatal(err)
	}
}

// writeStill lays twelve representative frames out in a 3x4 grid.
func writeStill(t *testing.T, path string, bg color.RGBA) {
	type shot struct {
		script []segment
		at     int // frame index to keep
	}
	rec := func(l float64) State { return State{Recording: true, Level: l} }
	shots := []shot{
		{[]segment{{1, Recording, fixed(rec(0.2))}}, 6}, // blooming in
		{[]segment{{2, Recording, fixed(rec(0.05))}}, 50},
		{[]segment{{2, Recording, speech(2)}}, 33},
		{[]segment{{2, Recording, fixed(rec(0.95))}}, 50},
		{[]segment{{2, Transcribing, fixed(State{Transcribing: true})}}, 40},
		{[]segment{{2, Transcribing, fixed(State{Transcribing: true})}}, 52},
		{[]segment{{2, Queued, fixed(State{Queued: 1})}}, 50},
		{[]segment{{2, Queued, fixed(State{Queued: 3})}}, 50},
		{[]segment{{2, Queued, fixed(State{Queued: 7})}}, 50},
		{[]segment{{1.5, Queued, fixed(State{Queued: 3})}, {1, Done, fixed(State{})}}, 45 + 5},
		{[]segment{{1.5, Queued, fixed(State{Queued: 3})}, {1, Done, fixed(State{})}}, 45 + 20},
		{[]segment{{1.5, Problem, fixed(State{})}}, 30},
	}
	var frames []*image.RGBA
	for _, sh := range shots {
		n := 0
		var keep []byte
		play(sh.script, func(px []byte) {
			if n == sh.at {
				keep = slices.Clone(px)
			}
			n++
		})
		frames = append(frames, frameImage(keep, bg))
	}
	writeSheet(t, path, frames, 3)
}

// BenchmarkFrame measures one step plus render while recording, the busiest
// state: every particle lit.
func BenchmarkFrame(b *testing.B) {
	var sc scene
	s := State{Recording: true, Level: 0.6}
	t := 0.0
	for i := 0; i < b.N; i++ {
		t += 1.0 / 30
		s.Level = 0.3 + 0.3*math.Sin(t*5)
		sc.step(Recording, s, t, 1.0/30)
		sc.render()
	}
}
