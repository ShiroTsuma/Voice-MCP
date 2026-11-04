package torus

import (
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const fps = 30

// speech is a level that rises from quiet to loud and back over d seconds,
// in syllables.
func speech(t, d float64) float64 {
	env := smooth(0.12*d, 0.42*d, t) * (1 - smooth(0.62*d, 0.92*d, t))
	syl := math.Pow(math.Abs(math.Sin(math.Pi*3.1*t)), 0.7) * (0.75 + 0.25*math.Sin(2*math.Pi*0.9*t+1))
	n := math.Sin(t*37.1) * math.Sin(t*23.3+2) * 0.08
	return clamp(0.04 + env*(0.25+0.75*syl) + n*env)
}

// film runs the scene on a script of states and keeps every frame.
type film struct {
	sc     scene
	base   time.Time
	t      float64
	frames [][]byte
}

func newFilm() *film { return &film{base: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

func (f *film) at(t float64) time.Time { return f.base.Add(time.Duration(t * float64(time.Second))) }

// run plays state(t) for d seconds; keep records the frames.
func (f *film) run(d float64, keep bool, state func(t float64) State) {
	for end := f.t + d - 1e-9; f.t < end; f.t += 1.0 / fps {
		s := state(f.t)
		f.sc.step(s.Show(f.at(f.t)), s, f.t, 1.0/fps)
		if keep {
			f.frames = append(f.frames, f.sc.render())
		}
	}
}

func steady(s State) func(float64) State { return func(float64) State { return s } }

func recordingFrom(start, d float64) func(float64) State {
	return func(t float64) State { return State{Recording: true, Level: speech(t-start, d)} }
}

// VOICE_OVERLAY_GIF=dir writes animated GIFs of every state and a still sheet.
func TestWriteGIFs(t *testing.T) {
	dir := os.Getenv("VOICE_OVERLAY_GIF")
	if dir == "" {
		t.Skip("set VOICE_OVERLAY_GIF=dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	darkBG := [3]byte{0x1e, 0x1e, 0x22}
	lightBG := [3]byte{0xf3, 0xf3, 0xf3}

	{
		f := newFilm()
		f.run(1, false, steady(State{Recording: true, Level: 0.04}))
		f.run(6, true, recordingFrom(1, 6))
		writeGIF(t, filepath.Join(dir, "recording.gif"), f.frames, darkBG)
	}
	{
		f := newFilm()
		f.run(1.5, false, steady(State{Transcribing: true}))
		f.run(3, true, steady(State{Transcribing: true}))
		writeGIF(t, filepath.Join(dir, "transcribing.gif"), f.frames, darkBG)
	}
	{
		f := newFilm()
		f.run(1.5, false, steady(State{Queued: 3}))
		f.run(4, true, steady(State{Queued: 3}))
		writeGIF(t, filepath.Join(dir, "queued.gif"), f.frames, darkBG)
	}
	{
		f := newFilm()
		f.run(1.5, false, steady(State{Queued: 3}))
		f.run(1.5, true, steady(State{Queued: 3}))
		done := State{Flash: Done, FlashUntil: f.at(f.t + 1.6)}
		f.run(2.6, true, steady(done))
		writeGIF(t, filepath.Join(dir, "deliver.gif"), f.frames, darkBG)
	}
	{
		f := newFilm()
		f.run(1, false, steady(State{Transcribing: true}))
		problem := State{Flash: Problem, FlashUntil: f.at(f.t + 1.6)}
		f.run(2.5, true, steady(problem))
		writeGIF(t, filepath.Join(dir, "problem.gif"), f.frames, darkBG)
	}
	seq := func() *film {
		f := newFilm()
		f.run(0.3, true, steady(State{}))
		f.run(4, true, recordingFrom(0.3, 4))
		f.run(2, true, steady(State{Transcribing: true}))
		f.run(2, true, steady(State{Queued: 2}))
		done := State{Flash: Done, FlashUntil: f.at(f.t + 1.6)}
		f.run(2.6, true, steady(done))
		return f
	}
	writeGIF(t, filepath.Join(dir, "sequence.gif"), seq().frames, darkBG)
	writeGIF(t, filepath.Join(dir, "sequence-light.gif"), seq().frames, lightBG)

	// The still sheet: 3 rows of 4 representative frames.
	var still [][]byte
	snap := func(f *film) { still = append(still, f.sc.render()) }
	for _, lv := range []float64{0.05, 0.45, 0.95} {
		f := newFilm()
		f.run(1.5, false, steady(State{Recording: true, Level: lv}))
		snap(f)
	}
	{
		f := newFilm()
		f.run(1.5, false, steady(State{Transcribing: true}))
		snap(f)
	}
	{
		f := newFilm()
		f.run(2.2, false, steady(State{Transcribing: true}))
		snap(f)
	}
	for _, n := range []int{1, 3, 7} {
		f := newFilm()
		f.run(1.5, false, steady(State{Queued: n}))
		snap(f)
	}
	{
		f := newFilm()
		f.run(1.5, false, steady(State{Queued: 3}))
		done := State{Flash: Done, FlashUntil: f.at(f.t + 1.6)}
		f.run(0.2, false, steady(done))
		snap(f)
		f.run(0.7, false, steady(done))
		snap(f)
	}
	{
		f := newFilm()
		f.run(1, false, steady(State{Transcribing: true}))
		problem := State{Flash: Problem, FlashUntil: f.at(f.t + 1.6)}
		f.run(0.8, false, steady(problem))
		snap(f)
	}
	{
		f := newFilm()
		f.run(0.25, false, steady(State{Recording: true, Level: 0.2}))
		snap(f)
	}
	writeSheet(t, filepath.Join(dir, "still.png"), still, 4, darkBG, scale)
}

// composite lays premultiplied BGRA over an opaque background, as RGB.
func composite(px []byte, bg [3]byte) []byte {
	out := make([]byte, Canvas*Canvas*3)
	for i := 0; i < Canvas*Canvas; i++ {
		a := int(px[i*4+3])
		for j, off := range [3]int{2, 1, 0} {
			out[i*3+j] = byte(min(255, int(px[i*4+off])+(int(bg[j])*(255-a)+127)/255))
		}
	}
	return out
}

const scale = 2

func writeGIF(t *testing.T, path string, frames [][]byte, bg [3]byte) {
	if strip := os.Getenv("VOICE_OVERLAY_STRIP"); strip != "" {
		// Every fourth frame at real size, to look at the motion frame by frame.
		var some [][]byte
		for i, px := range frames {
			if i%4 == 0 {
				some = append(some, px)
			}
		}
		name := strings.TrimSuffix(filepath.Base(path), ".gif") + ".png"
		writeSheet(t, filepath.Join(strip, name), some, 12, bg, 1)
	}
	rgbs := make([][]byte, len(frames))
	for i, px := range frames {
		rgbs[i] = composite(px, bg)
	}
	pal := palette(rgbs)
	full := append(color.Palette{}, pal...)
	full = append(full, color.RGBA{}) // the last entry: unchanged since the last frame
	unchanged := uint8(len(full) - 1)
	m := newMatcher(pal)
	w := Canvas * scale
	g := &gif.GIF{Config: image.Config{ColorModel: full, Width: w, Height: w}}
	var prev []uint8
	for n, c := range rgbs {
		img := image.NewPaletted(image.Rect(0, 0, w, w), full)
		for y := 0; y < w; y++ {
			for x := 0; x < w; x++ {
				i := (y/scale)*Canvas + x/scale
				d := (bayer8[(y&7)*8+(x&7)]+0.5)/64 - 0.5
				img.Pix[y*w+x] = m.index(c[i*3:i*3+3], d*5)
			}
		}
		cur := append([]uint8(nil), img.Pix...)
		if prev != nil {
			for i := range img.Pix {
				if img.Pix[i] == prev[i] {
					img.Pix[i] = unchanged
				}
			}
		}
		prev = cur
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, []int{3, 3, 4}[n%3]) // 30 fps in hundredths
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

func writeSheet(t *testing.T, path string, frames [][]byte, cols int, bg [3]byte, k int) {
	const gap = 8
	cell := Canvas * k
	rows := (len(frames) + cols - 1) / cols
	img := image.NewRGBA(image.Rect(0, 0, cols*cell+(cols+1)*gap, rows*cell+(rows+1)*gap))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = bg[0], bg[1], bg[2], 255
	}
	for n, px := range frames {
		rgb := composite(px, bg)
		ox, oy := gap+(n%cols)*(cell+gap), gap+(n/cols)*(cell+gap)
		for y := 0; y < cell; y++ {
			for x := 0; x < cell; x++ {
				i := (y/k)*Canvas + x/k
				img.Set(ox+x, oy+y, color.RGBA{rgb[i*3], rgb[i*3+1], rgb[i*3+2], 255})
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

var bayer8 = func() [64]float64 {
	var m [64]float64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			v, xc := 0, x^y
			for bit := 2; bit >= 0; bit-- {
				v = v<<2 | (xc>>bit&1)<<1 | (y >> bit & 1)
			}
			m[y*8+x] = float64(v)
		}
	}
	return m
}()

type entry struct {
	c [3]float64
	n float64
}

// palette picks 255 colours for all frames together: median cut over the
// colours weighted by how often they appear, then a few rounds of k-means.
func palette(frames [][]byte) color.Palette {
	count := map[[3]byte]int{}
	for _, f := range frames {
		for i := 0; i < len(f); i += 3 {
			count[[3]byte{f[i], f[i+1], f[i+2]}]++
		}
	}
	es := make([]entry, 0, len(count))
	for c, n := range count {
		es = append(es, entry{[3]float64{float64(c[0]), float64(c[1]), float64(c[2])}, float64(n)})
	}
	const want = 255
	boxes := [][]entry{es}
	for len(boxes) < want {
		best, bestScore, bestCh := -1, 0.0, 0
		for i, b := range boxes {
			if len(b) < 2 {
				continue
			}
			ch, rng, pop := 0, 0.0, 0.0
			for j := range 3 {
				lo, hi := 256.0, -1.0
				for _, e := range b {
					lo, hi = math.Min(lo, e.c[j]), math.Max(hi, e.c[j])
				}
				if hi-lo > rng {
					ch, rng = j, hi-lo
				}
			}
			for _, e := range b {
				pop += e.n
			}
			if s := rng * math.Sqrt(pop); s > bestScore {
				best, bestScore, bestCh = i, s, ch
			}
		}
		if best < 0 {
			break
		}
		b := boxes[best]
		sort.Slice(b, func(i, j int) bool { return b[i].c[bestCh] < b[j].c[bestCh] })
		var tot, acc float64
		for _, e := range b {
			tot += e.n
		}
		cut := 1
		for i, e := range b {
			acc += e.n
			if acc >= tot/2 {
				cut = max(1, min(len(b)-1, i+1))
				break
			}
		}
		boxes[best] = b[:cut]
		boxes = append(boxes, b[cut:])
	}
	cents := make([][3]float64, len(boxes))
	for i, b := range boxes {
		var s [3]float64
		var n float64
		for _, e := range b {
			for j := range 3 {
				s[j] += e.c[j] * e.n
			}
			n += e.n
		}
		for j := range 3 {
			cents[i][j] = s[j] / n
		}
	}
	for range 4 {
		var sums [][4]float64 = make([][4]float64, len(cents))
		for _, e := range es {
			k := nearest(cents, e.c)
			for j := range 3 {
				sums[k][j] += e.c[j] * e.n
			}
			sums[k][3] += e.n
		}
		for k := range cents {
			if sums[k][3] > 0 {
				for j := range 3 {
					cents[k][j] = sums[k][j] / sums[k][3]
				}
			}
		}
	}
	pal := make(color.Palette, len(cents))
	for i, c := range cents {
		pal[i] = color.RGBA{uint8(math.Round(c[0])), uint8(math.Round(c[1])), uint8(math.Round(c[2])), 255}
	}
	return pal
}

func nearest(cents [][3]float64, c [3]float64) int {
	best, bd := 0, math.Inf(1)
	for k, p := range cents {
		dr, dg, db := c[0]-p[0], c[1]-p[1], c[2]-p[2]
		d := 2*dr*dr + 4*dg*dg + 3*db*db
		if d < bd {
			best, bd = k, d
		}
	}
	return best
}

// matcher finds the nearest palette colour of a dithered pixel, cached.
type matcher struct {
	cents [][3]float64
	cache []int16
}

func newMatcher(p color.Palette) *matcher {
	m := &matcher{cache: make([]int16, 1<<21)}
	for i := range m.cache {
		m.cache[i] = -1
	}
	for _, c := range p {
		r, g, b, _ := c.RGBA()
		m.cents = append(m.cents, [3]float64{float64(r >> 8), float64(g >> 8), float64(b >> 8)})
	}
	return m
}

func (m *matcher) index(c []byte, d float64) uint8 {
	var q [3]int
	for j := range 3 {
		q[j] = max(0, min(255, int(math.Round(float64(c[j])+d)))) >> 1
	}
	key := q[0]<<14 | q[1]<<7 | q[2]
	if v := m.cache[key]; v >= 0 {
		return uint8(v)
	}
	k := nearest(m.cents, [3]float64{float64(q[0]*2 + 1), float64(q[1]*2 + 1), float64(q[2]*2 + 1)})
	m.cache[key] = int16(k)
	return uint8(k)
}
