package aurora

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

// VOICE_OVERLAY_GIF=dir writes animated GIFs of every state (30 fps, twice
// the real size) and a still grid, to look at.
func TestWriteGIFs(t *testing.T) {
	dir := os.Getenv("VOICE_OVERLAY_GIF")
	if dir == "" {
		t.Skip("set VOICE_OVERLAY_GIF=dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dark := [3]uint8{0x1e, 0x1e, 0x22}
	light := [3]uint8{0xf3, 0xf3, 0xf3}
	write := func(name string, bg [3]uint8, frames [][]byte) {
		if err := writeGIF(filepath.Join(dir, name), bg, frames); err != nil {
			t.Fatal(err)
		}
	}
	write("recording.gif", dark, film(0, 6, false, func(t float64) State {
		return State{Recording: true, Level: speech(t, 6)}
	}))
	write("transcribing.gif", dark, film(1.5, 3, false, func(float64) State { return State{Transcribing: true} }))
	write("queued.gif", dark, film(1.5, 4, false, func(float64) State { return State{Queued: 3} }))
	write("deliver.gif", dark, film(1.5, 1.5, true, func(t float64) State {
		if t < 3 {
			return State{Queued: 3}
		}
		return State{Flash: Done, FlashUntil: at(3 + 1.6)}
	}))
	write("problem.gif", dark, film(0, 2.5, true, func(float64) State {
		return State{Flash: Problem, FlashUntil: at(1.6)}
	}))
	seq := film(0, 10, true, sequence)
	write("sequence.gif", dark, seq)
	write("sequence-light.gif", light, seq)
	writeStill(t, filepath.Join(dir, "still.png"), dark)
}

// sequence is a whole dictation: silence, speech, transcription, a busy agent
// with two waiting messages, delivery.
func sequence(t float64) State {
	switch {
	case t < 0.3:
		return State{}
	case t < 4.3:
		return State{Recording: true, Level: speech(t-0.3, 4)}
	case t < 6.3:
		return State{Transcribing: true}
	case t < 8.3:
		return State{Queued: 2}
	}
	return State{Flash: Done, FlashUntil: at(8.3 + 1.6)}
}

var epoch = time.Unix(1_700_000_000, 0)

func at(t float64) time.Time { return epoch.Add(time.Duration(t * float64(time.Second))) }

// speech is a microphone level like a sentence over T seconds: quiet, loud,
// quiet, with syllables and short pauses between words.
func speech(t, T float64) float64 {
	env := math.Pow(math.Sin(math.Pi*clamp(t/T)), 1.3)
	syl := 0.5 + 0.5*math.Sin(2*math.Pi*3.3*t+2.2*math.Sin(2*math.Pi*0.8*t))
	words := smooth(0.05, 0.3, math.Abs(math.Sin(math.Pi*t/1.35+0.4)))
	return clamp((0.08 + 0.95*env*(0.25+0.75*syl*syl)) * (0.35 + 0.65*words))
}

// film runs the scene through script at 30 fps; frames before skip seconds
// are run but not kept. With untilGone it keeps going past seconds until the
// overlay has hidden itself, then holds a few clear frames.
func film(skip, seconds float64, untilGone bool, script func(t float64) State) [][]byte {
	var sc scene
	var frames [][]byte
	for f := 0; ; f++ {
		t := float64(f) / 30
		s := script(t)
		vis := sc.step(s.Show(at(t)), s, t, 1.0/30)
		if t >= skip {
			frames = append(frames, sc.render())
		}
		end := skip + seconds
		if !untilGone && t+1.0/30 >= end-1e-9 {
			break
		}
		if untilGone && t >= end && !vis {
			for i := 0; i < 8; i++ {
				frames = append(frames, sc.render())
			}
			break
		}
		if t > end+10 {
			break
		}
	}
	return frames
}

// over composites a premultiplied frame onto bg, scaled up twice.
func over(px []byte, bg [3]uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, Canvas*2, Canvas*2))
	for y := 0; y < Canvas; y++ {
		for x := 0; x < Canvas; x++ {
			i := (y*Canvas + x) * 4
			a := int(px[i+3])
			var c [3]uint8
			for j, off := range [3]int{2, 1, 0} {
				c[j] = uint8(min(255, int(px[i+off])+(int(bg[j])*(255-a)+127)/255))
			}
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					o := img.PixOffset(2*x+dx, 2*y+dy)
					img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = c[0], c[1], c[2], 255
				}
			}
		}
	}
	return img
}

func writeGIF(path string, bg [3]uint8, frames [][]byte) error {
	imgs := make([]*image.RGBA, len(frames))
	for i, f := range frames {
		imgs[i] = over(f, bg)
	}
	pal := medianCut(imgs, bg)
	q := newQuantizer(pal)
	g := &gif.GIF{Config: image.Config{ColorModel: pal, Width: Canvas * 2, Height: Canvas * 2}}
	for i, img := range imgs {
		g.Image = append(g.Image, q.dither(img))
		g.Delay = append(g.Delay, []int{3, 3, 4}[i%3]) // 30 fps in hundredths
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return gif.EncodeAll(f, g)
}

// medianCut picks 256 colours for all the frames, with bg among them exactly.
func medianCut(imgs []*image.RGBA, bg [3]uint8) color.Palette {
	type px struct {
		c [3]uint8
		n int
	}
	hist := map[[3]uint8]int{}
	for _, img := range imgs {
		for o := 0; o < len(img.Pix); o += 8 { // every other pixel: scaled twice
			hist[[3]uint8{img.Pix[o], img.Pix[o+1], img.Pix[o+2]}]++
		}
	}
	delete(hist, bg)
	all := make([]px, 0, len(hist))
	for c, n := range hist {
		all = append(all, px{c, n})
	}
	type box struct{ p []px }
	score := func(b box) (float64, int) {
		var lo, hi [3]int
		lo = [3]int{255, 255, 255}
		n := 0
		for _, p := range b.p {
			for j := range 3 {
				lo[j] = min(lo[j], int(p.c[j]))
				hi[j] = max(hi[j], int(p.c[j]))
			}
			n += p.n
		}
		axis, rng := 0, -1
		for j := range 3 {
			if hi[j]-lo[j] > rng {
				axis, rng = j, hi[j]-lo[j]
			}
		}
		if len(b.p) < 2 || rng == 0 {
			return -1, axis
		}
		return float64(rng) * math.Pow(float64(n), 0.45), axis
	}
	boxes := []box{{all}}
	for len(boxes) < 255 {
		best, bs, ba := -1, 0.0, 0
		for i, b := range boxes {
			if s, a := score(b); s > bs {
				best, bs, ba = i, s, a
			}
		}
		if best < 0 {
			break
		}
		b := boxes[best].p
		sort.Slice(b, func(i, j int) bool { return b[i].c[ba] < b[j].c[ba] })
		total := 0
		for _, p := range b {
			total += p.n
		}
		cut, acc := 1, 0
		for i, p := range b {
			acc += p.n
			if acc*2 >= total {
				cut = min(max(i+1, 1), len(b)-1)
				break
			}
		}
		boxes[best] = box{b[:cut]}
		boxes = append(boxes, box{b[cut:]})
	}
	pal := color.Palette{color.RGBA{bg[0], bg[1], bg[2], 255}}
	for _, b := range boxes {
		var s [3]float64
		n := 0.0
		for _, p := range b.p {
			for j := range 3 {
				s[j] += float64(p.c[j]) * float64(p.n)
			}
			n += float64(p.n)
		}
		if n == 0 {
			continue
		}
		pal = append(pal, color.RGBA{uint8(s[0]/n + 0.5), uint8(s[1]/n + 0.5), uint8(s[2]/n + 0.5), 255})
	}
	return pal
}

// quantizer maps colours to the palette through a cache, with ordered
// dithering: stable from frame to frame, unlike error diffusion.
type quantizer struct {
	pal  color.Palette
	rgb  [][3]int
	lut  []int16
	bayr [64]float64
}

func newQuantizer(pal color.Palette) *quantizer {
	q := &quantizer{pal: pal, lut: make([]int16, 1<<21)}
	for i := range q.lut {
		q.lut[i] = -1
	}
	for _, c := range pal {
		r, g, b, _ := c.RGBA()
		q.rgb = append(q.rgb, [3]int{int(r >> 8), int(g >> 8), int(b >> 8)})
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			v, xx, yy := 0, x, y
			for bit := 0; bit < 3; bit++ {
				v = v<<2 | ((xx&1)^(yy&1))<<1 | (yy & 1)
				xx >>= 1
				yy >>= 1
			}
			q.bayr[y*8+x] = (float64(v)+0.5)/64 - 0.5
		}
	}
	return q
}

func (q *quantizer) index(r, g, b int) uint8 {
	k := (r>>1)<<14 | (g>>1)<<7 | b>>1
	if v := q.lut[k]; v >= 0 {
		return uint8(v)
	}
	best, bd := 0, math.MaxInt
	for i, c := range q.rgb {
		dr, dg, db := c[0]-r, c[1]-g, c[2]-b
		d := 3*dr*dr + 4*dg*dg + 2*db*db
		if d < bd {
			best, bd = i, d
		}
	}
	q.lut[k] = int16(best)
	return uint8(best)
}

func (q *quantizer) dither(img *image.RGBA) *image.Paletted {
	b := img.Bounds()
	out := image.NewPaletted(b, q.pal)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			o := img.PixOffset(x, y)
			d := q.bayr[(y&7)*8+x&7] * 7
			c := func(v uint8) int { return min(255, max(0, int(float64(v)+d+0.5))) }
			out.Pix[y*out.Stride+x] = q.index(c(img.Pix[o]), c(img.Pix[o+1]), c(img.Pix[o+2]))
		}
	}
	return out
}

// writeStill lays out representative frames in a grid of 3 by 4.
func writeStill(t *testing.T, path string, bg [3]uint8) {
	pick := func(skip float64, script func(float64) State) []byte {
		f := film(skip, 1.0/30, false, script)
		return f[len(f)-1]
	}
	rec := func(l float64) func(float64) State {
		return func(float64) State { return State{Recording: true, Level: l} }
	}
	queued := func(n int) func(float64) State {
		return func(float64) State { return State{Queued: n} }
	}
	deliver := func(t float64) State {
		if t < 2 {
			return State{Queued: 3}
		}
		return State{Flash: Done, FlashUntil: at(3.6)}
	}
	frames := [][]byte{
		pick(0.25, rec(0.2)), pick(2, rec(0.1)), pick(2, rec(0.55)),
		pick(2, rec(1)), pick(2, func(float64) State { return State{Transcribing: true} }), pick(2, queued(1)),
		pick(2, queued(3)), pick(2, queued(7)), pick(2.25, deliver),
		pick(3.2, deliver), pick(1.2, func(float64) State { return State{Flash: Problem, FlashUntil: at(1.6)} }), pick(3.9, deliver),
	}
	const gap = 8
	w := 3*Canvas*2 + 4*gap
	h := 4*Canvas*2 + 5*gap
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = bg[0], bg[1], bg[2], 255
	}
	for n, f := range frames {
		x0, y0 := gap+(n%3)*(Canvas*2+gap), gap+(n/3)*(Canvas*2+gap)
		tile := over(f, bg)
		for y := 0; y < Canvas*2; y++ {
			copy(img.Pix[img.PixOffset(x0, y0+y):], tile.Pix[tile.PixOffset(0, y):tile.PixOffset(Canvas*2, y)])
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}
