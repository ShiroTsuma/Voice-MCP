package liquid

import (
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// speech is a speech-like microphone level over dur seconds: quiet, a loud
// middle, quiet again, with syllables and short pauses.
func speech(t, dur float64) float64 {
	env := math.Pow(math.Max(0, math.Sin(math.Pi*t/dur)), 1.3)
	syl := math.Abs(math.Sin(t*2*math.Pi*2.6)) * (0.65 + 0.35*math.Sin(t*7.3+1))
	pause := smooth(0.15, 0.4, math.Abs(math.Sin(t*2*math.Pi*0.37+0.5)))
	return clamp(0.03 + env*(0.25+0.75*syl)*pause)
}

// timeline says what the overlay shows at t seconds.
type timeline func(t float64) (Kind, State)

const fps = 30

// film runs a scene through tl for dur seconds (or, with dur 0, until it
// hides) after warm seconds off camera, and returns the frames.
func film(tl timeline, warm, dur float64) [][]byte {
	var sc scene
	dt := 1.0 / fps
	for t := -warm; t < 0; t += dt {
		k, s := tl(t)
		sc.step(k, s, t+100, dt)
	}
	var frames [][]byte
	seen := false
	for i := 0; ; i++ {
		t := float64(i) * dt
		if dur > 0 && t >= dur-1e-9 {
			break
		}
		k, s := tl(t)
		vis := sc.step(k, s, t+100, dt)
		frames = append(frames, sc.render())
		seen = seen || vis
		if dur == 0 && seen && !vis {
			break
		}
		if i > 60*fps {
			break
		}
	}
	return frames
}

func recordingTL(dur float64) timeline {
	return func(t float64) (Kind, State) {
		if t < 0 {
			return Recording, State{Recording: true, Level: 0.03}
		}
		return Recording, State{Recording: true, Level: speech(t, dur)}
	}
}

func steady(k Kind, s State) timeline {
	return func(float64) (Kind, State) { return k, s }
}

// flash shows kind for 1.6 s from t0, then nothing (as the window would).
func flashAt(k Kind, t, t0 float64) Kind {
	if t-t0 < 1.6 {
		return k
	}
	return Hidden
}

func sequenceTL(t float64) (Kind, State) {
	switch {
	case t < 0.3:
		return Hidden, State{}
	case t < 4.3:
		return Recording, State{Recording: true, Level: speech(t-0.3, 4)}
	case t < 6.3:
		return Transcribing, State{Transcribing: true}
	case t < 8.3:
		return Queued, State{Queued: 2}
	}
	return flashAt(Done, t, 8.3), State{}
}

// composite lays premultiplied BGRA over bg, scaled up by scale.
func composite(buf []byte, bg [3]float64, scale int) *image.RGBA {
	n := Canvas * scale
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < Canvas; y++ {
		for x := 0; x < Canvas; x++ {
			i := (y*Canvas + x) * 4
			a := float64(buf[i+3]) / 255
			c := [3]float64{float64(buf[i+2]) / 255, float64(buf[i+1]) / 255, float64(buf[i]) / 255}
			var o [4]uint8
			for j := 0; j < 3; j++ {
				o[j] = uint8(math.Round(255 * clamp(c[j]+(1-a)*bg[j])))
			}
			o[3] = 255
			for sy := 0; sy < scale; sy++ {
				for sx := 0; sx < scale; sx++ {
					k := img.PixOffset(x*scale+sx, y*scale+sy)
					copy(img.Pix[k:k+4], o[:])
				}
			}
		}
	}
	return img
}

// palette is a median cut of the frames' colours, with bg kept exact and the
// last entry left transparent for unchanged pixels.
func palette(imgs []*image.RGBA, bg color.RGBA) color.Palette {
	hist := make([]int32, 1<<18)
	for i, im := range imgs {
		if i%2 == 1 {
			continue
		}
		p := im.Pix
		for k := 0; k < len(p); k += 4 * 2 {
			hist[int(p[k]>>2)<<12|int(p[k+1]>>2)<<6|int(p[k+2]>>2)]++
		}
	}
	type entry struct {
		c [3]int
		n int32
	}
	var all []entry
	for k, n := range hist {
		if n > 0 {
			all = append(all, entry{[3]int{k >> 12, k >> 6 & 63, k & 63}, n})
		}
	}
	type box struct{ e []entry }
	stats := func(b box) (axis, span int, w float64) {
		lo, hi := [3]int{64, 64, 64}, [3]int{-1, -1, -1}
		for _, e := range b.e {
			for j := 0; j < 3; j++ {
				lo[j] = min(lo[j], e.c[j])
				hi[j] = max(hi[j], e.c[j])
			}
			w += float64(e.n)
		}
		for j := 0; j < 3; j++ {
			if hi[j]-lo[j] > span {
				axis, span = j, hi[j]-lo[j]
			}
		}
		return
	}
	boxes := []box{{all}}
	for len(boxes) < 254 {
		best, bestScore := -1, 0.0
		for i, b := range boxes {
			_, span, w := stats(b)
			if span == 0 || len(b.e) < 2 {
				continue
			}
			// Favour long boxes over crowded ones so gradients keep their steps.
			score := float64(span) * math.Pow(w, 0.35)
			if score > bestScore {
				best, bestScore = i, score
			}
		}
		if best < 0 {
			break
		}
		b := boxes[best]
		axis, _, w := stats(b)
		sort.Slice(b.e, func(i, j int) bool { return b.e[i].c[axis] < b.e[j].c[axis] })
		acc, cut := 0.0, 1
		for i, e := range b.e {
			acc += float64(e.n)
			if acc >= w/2 {
				cut = max(1, min(len(b.e)-1, i))
				break
			}
		}
		boxes[best] = box{b.e[:cut]}
		boxes = append(boxes, box{b.e[cut:]})
	}
	pal := color.Palette{bg}
	for _, b := range boxes {
		var s [3]float64
		var w float64
		for _, e := range b.e {
			for j := 0; j < 3; j++ {
				s[j] += float64(e.c[j]*4+2) * float64(e.n)
			}
			w += float64(e.n)
		}
		pal = append(pal, color.RGBA{uint8(s[0] / w), uint8(s[1] / w), uint8(s[2] / w), 255})
	}
	return append(pal, color.RGBA{})
}

// quantizer dithers frames onto a palette (Floyd-Steinberg) with a cache of
// nearest colours.
type quantizer struct {
	pal   color.Palette
	rgb   [][3]float64
	cache []int16
}

func newQuantizer(pal color.Palette) *quantizer {
	q := &quantizer{pal: pal, cache: make([]int16, 1<<18)}
	for i := range q.cache {
		q.cache[i] = -1
	}
	for _, c := range pal[:len(pal)-1] {
		r, g, b, _ := c.RGBA()
		q.rgb = append(q.rgb, [3]float64{float64(r >> 8), float64(g >> 8), float64(b >> 8)})
	}
	return q
}

func (q *quantizer) nearest(r, g, b float64) int {
	ri, gi, bi := int(clamp(r/255)*255)>>2, int(clamp(g/255)*255)>>2, int(clamp(b/255)*255)>>2
	k := ri<<12 | gi<<6 | bi
	if v := q.cache[k]; v >= 0 {
		return int(v)
	}
	cr, cg, cb := float64(ri*4+2), float64(gi*4+2), float64(bi*4+2)
	best, bd := 0, math.MaxFloat64
	for i, c := range q.rgb {
		d := (c[0]-cr)*(c[0]-cr)*0.9 + (c[1]-cg)*(c[1]-cg)*1.2 + (c[2]-cb)*(c[2]-cb)*0.8
		if d < bd {
			best, bd = i, d
		}
	}
	q.cache[k] = int16(best)
	return best
}

func (q *quantizer) frame(im *image.RGBA) []uint8 {
	w, h := im.Rect.Dx(), im.Rect.Dy()
	out := make([]uint8, w*h)
	cur := make([][3]float64, w+2)
	next := make([][3]float64, w+2)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			k := im.PixOffset(x, y)
			var v [3]float64
			for j := 0; j < 3; j++ {
				v[j] = float64(im.Pix[k+j]) + cur[x+1][j]
			}
			idx := q.nearest(v[0], v[1], v[2])
			out[y*w+x] = uint8(idx)
			for j := 0; j < 3; j++ {
				e := v[j] - q.rgb[idx][j]
				cur[x+2][j] += e * 7 / 16
				next[x][j] += e * 3 / 16
				next[x+1][j] += e * 5 / 16
				next[x+2][j] += e * 1 / 16
			}
		}
		cur, next = next, cur
		for i := range next {
			next[i] = [3]float64{}
		}
	}
	return out
}

func writeGIF(t *testing.T, path string, frames [][]byte, bg [3]float64) {
	imgs := make([]*image.RGBA, len(frames))
	for i, f := range frames {
		imgs[i] = composite(f, bg, 2)
	}
	bgc := color.RGBA{uint8(math.Round(bg[0] * 255)), uint8(math.Round(bg[1] * 255)), uint8(math.Round(bg[2] * 255)), 255}
	pal := palette(imgs, bgc)
	q := newQuantizer(pal)
	trans := uint8(len(pal) - 1)
	g := &gif.GIF{LoopCount: 0}
	var prev []uint8
	n := Canvas * 2
	for i, im := range imgs {
		idx := q.frame(im)
		pi := image.NewPaletted(image.Rect(0, 0, n, n), pal)
		copy(pi.Pix, idx)
		if prev != nil {
			for k := range idx {
				if idx[k] == prev[k] {
					pi.Pix[k] = trans
				}
			}
		}
		prev = idx
		g.Image = append(g.Image, pi)
		g.Delay = append(g.Delay, []int{3, 3, 4}[i%3])
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := gif.EncodeAll(f, g); err != nil {
		t.Fatal(err)
	}
}

func writePNG(t *testing.T, path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// sheet lays frames out in a grid, every step-th frame.
func sheet(frames [][]byte, bg [3]float64, step, cols, scale int) *image.RGBA {
	var pick [][]byte
	for i := 0; i < len(frames); i += step {
		pick = append(pick, frames[i])
	}
	rows := (len(pick) + cols - 1) / cols
	n := Canvas * scale
	out := image.NewRGBA(image.Rect(0, 0, cols*n, rows*n))
	for i, f := range pick {
		im := composite(f, bg, scale)
		ox, oy := i%cols*n, i/cols*n
		for y := 0; y < n; y++ {
			copy(out.Pix[out.PixOffset(ox, oy+y):], im.Pix[im.PixOffset(0, y):im.PixOffset(n, y)])
		}
	}
	return out
}

var (
	dark  = [3]float64{0x1e / 255.0, 0x1e / 255.0, 0x22 / 255.0}
	light = [3]float64{0xf3 / 255.0, 0xf3 / 255.0, 0xf3 / 255.0}
)

// VOICE_OVERLAY_GIF=dir writes animated GIFs of every state and a still grid;
// VOICE_OVERLAY_SHEETS=dir also writes contact sheets of them, to review.
func TestWriteGIFs(t *testing.T) {
	dir := os.Getenv("VOICE_OVERLAY_GIF")
	if dir == "" {
		t.Skip("set VOICE_OVERLAY_GIF=dir")
	}
	sheets := os.Getenv("VOICE_OVERLAY_SHEETS")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	type clip struct {
		name      string
		tl        timeline
		warm, dur float64
		bg        [3]float64
	}
	clips := []clip{
		{"recording", recordingTL(6), 1.2, 6, dark},
		{"transcribing", steady(Transcribing, State{Transcribing: true}), 1.2, 3, dark},
		{"queued", steady(Queued, State{Queued: 3}), 1.5, 4, dark},
		{"deliver", func(t float64) (Kind, State) {
			if t < 1.5 {
				return Queued, State{Queued: 3}
			}
			return flashAt(Done, t, 1.5), State{}
		}, 1.5, 0, dark},
		{"problem", func(t float64) (Kind, State) {
			if t < 0 {
				return Transcribing, State{Transcribing: true}
			}
			if t < 0.3 {
				return Transcribing, State{Transcribing: true}
			}
			return flashAt(Problem, t, 0.3), State{}
		}, 1.2, 2.5, dark},
		{"sequence", sequenceTL, 0, 0, dark},
		{"sequence-light", sequenceTL, 0, 0, light},
	}
	for _, c := range clips {
		start := time.Now()
		frames := film(c.tl, c.warm, c.dur)
		writeGIF(t, filepath.Join(dir, c.name+".gif"), frames, c.bg)
		if sheets != "" {
			os.MkdirAll(sheets, 0o755)
			writePNG(t, filepath.Join(sheets, c.name+".png"), sheet(frames, c.bg, 4, 10, 1))
		}
		t.Logf("%s: %d frames in %v", c.name, len(frames), time.Since(start))
	}
	writePNG(t, filepath.Join(dir, "still.png"), stills())
}

// stills is a 3x4 grid of representative moments, 2x, on dark.
func stills() *image.RGBA {
	at := func(tl timeline, warm, t float64) []byte {
		f := film(tl, warm, t+1.0/fps)
		return f[len(f)-1]
	}
	deliver := func(t float64) (Kind, State) {
		if t < 0 {
			return Queued, State{Queued: 3}
		}
		return flashAt(Done, t, 0), State{}
	}
	problem := func(t float64) (Kind, State) {
		if t < 0 {
			return Transcribing, State{Transcribing: true}
		}
		return Problem, State{}
	}
	lvl := func(l float64) timeline {
		return func(t float64) (Kind, State) { return Recording, State{Recording: true, Level: l} }
	}
	frames := [][]byte{
		at(lvl(0.03), 2, 0),
		at(recordingTL(6), 1, 2.2),
		at(lvl(0.95), 2, 0),
		at(steady(Transcribing, State{Transcribing: true}), 2, 0),
		at(steady(Transcribing, State{Transcribing: true}), 2, 0.25),
		at(steady(Queued, State{Queued: 1}), 2, 0),
		at(steady(Queued, State{Queued: 3}), 2, 0),
		at(steady(Queued, State{Queued: 7}), 2, 0),
		at(deliver, 2, 0.2),
		at(deliver, 2, 0.45),
		at(deliver, 2, 1.1),
		at(problem, 1.5, 0.5),
	}
	return sheet(frames, dark, 1, 4, 2)
}
