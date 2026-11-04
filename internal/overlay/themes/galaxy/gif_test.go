package galaxy

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

var clock0 = time.Unix(1_800_000_000, 0)

func at(t float64) time.Time { return clock0.Add(time.Duration(t * float64(time.Second))) }

// speech is a speech-like microphone level: syllables and words under an
// envelope that goes quiet, loud and quiet again over T seconds.
func speech(t, T float64) float64 {
	env := math.Pow(math.Sin(math.Pi*clamp(t/T)), 1.3)
	syll := 0.5 + 0.5*math.Sin(t*2*math.Pi*3.3+1.3*math.Sin(t*1.7))
	word := smooth(-0.35, 0.35, math.Sin(t*2*math.Pi*0.62+0.4))
	return clamp(0.04 + 0.96*env*(0.2+0.8*syll)*(0.3+0.7*word))
}

// timeline is what the dictation shows at t seconds.
type timeline func(t float64) State

// film runs sc through tl from t0 to t1 (or, with t1 < 0, until it hides)
// and returns the frames.
func film(sc *scene, tl timeline, t0, t1 float64) [][]byte {
	var frames [][]byte
	seen := false
	for t := t0; t1 < 0 || t < t1-1e-9; t += 1.0 / 30 {
		s := tl(t)
		vis := sc.step(s.Show(at(t)), s, t, 1.0/30)
		frames = append(frames, sc.render())
		seen = seen || vis
		if t1 < 0 && seen && !vis {
			frames = append(frames, frames[len(frames)-1], frames[len(frames)-1])
			break
		}
		if t-t0 > 20 {
			break
		}
	}
	return frames
}

func recordingTL(T float64) timeline {
	return func(t float64) State { return State{Recording: true, Level: speech(t, T)} }
}

func flashTL(base State, k Kind, start float64) timeline {
	return func(t float64) State {
		s := base
		s.Flash, s.FlashUntil = k, at(start+1.6)
		return s
	}
}

func sequenceTL(t float64) State {
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

func compose(px []byte, bg [3]uint8) *image.RGBA { return composeAt(px, bg, 2) }

func composeAt(px []byte, bg [3]uint8, s int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, Canvas*s, Canvas*s))
	for y := 0; y < Canvas; y++ {
		for x := 0; x < Canvas; x++ {
			i := (y*Canvas + x) * 4
			a := int(px[i+3])
			c := [3]uint8{
				uint8(min(255, int(px[i+2])+int(bg[0])*(255-a)/255)),
				uint8(min(255, int(px[i+1])+int(bg[1])*(255-a)/255)),
				uint8(min(255, int(px[i])+int(bg[2])*(255-a)/255)),
			}
			for dy := 0; dy < s; dy++ {
				for dx := 0; dx < s; dx++ {
					o := img.PixOffset(x*s+dx, y*s+dy)
					img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = c[0], c[1], c[2], 255
				}
			}
		}
	}
	return img
}

// medianCut builds a palette of n colours (bg included exactly) from imgs.
func medianCut(imgs []*image.RGBA, n int, bg [3]uint8) color.Palette {
	hist := map[uint32]int{}
	for _, im := range imgs {
		for o := 0; o < len(im.Pix); o += 4 * 2 { // every other pixel: upscaled anyway
			k := uint32(im.Pix[o])<<16 | uint32(im.Pix[o+1])<<8 | uint32(im.Pix[o+2])
			hist[k]++
		}
	}
	bgk := uint32(bg[0])<<16 | uint32(bg[1])<<8 | uint32(bg[2])
	delete(hist, bgk)
	type entry struct {
		c [3]int
		n int
	}
	var all []entry
	for k, v := range hist {
		all = append(all, entry{[3]int{int(k >> 16), int(k >> 8 & 255), int(k & 255)}, v})
	}
	type box struct{ e []entry }
	boxes := []box{{all}}
	rangeOf := func(b box) (int, int, int) {
		lo, hi := [3]int{255, 255, 255}, [3]int{}
		cnt := 0
		for _, e := range b.e {
			cnt += e.n
			for j := 0; j < 3; j++ {
				lo[j], hi[j] = min(lo[j], e.c[j]), max(hi[j], e.c[j])
			}
		}
		ax, r := 0, -1
		for j := 0; j < 3; j++ {
			if hi[j]-lo[j] > r {
				ax, r = j, hi[j]-lo[j]
			}
		}
		return ax, r, cnt
	}
	for len(boxes) < n-1 {
		best, bestScore, bestAx := -1, 0.0, 0
		for i, b := range boxes {
			if len(b.e) < 2 {
				continue
			}
			ax, r, cnt := rangeOf(b)
			score := float64(r) * math.Sqrt(float64(cnt))
			if score > bestScore {
				best, bestScore, bestAx = i, score, ax
			}
		}
		if best < 0 {
			break
		}
		e := boxes[best].e
		sort.Slice(e, func(i, j int) bool { return e[i].c[bestAx] < e[j].c[bestAx] })
		tot := 0
		for _, x := range e {
			tot += x.n
		}
		acc, cut := 0, 1
		for i, x := range e {
			acc += x.n
			if acc*2 >= tot {
				cut = max(1, min(i+1, len(e)-1))
				break
			}
		}
		boxes[best] = box{e[:cut]}
		boxes = append(boxes, box{e[cut:]})
	}
	pal := color.Palette{color.RGBA{bg[0], bg[1], bg[2], 255}}
	for _, b := range boxes {
		var s [3]float64
		cnt := 0.0
		for _, e := range b.e {
			for j := 0; j < 3; j++ {
				s[j] += float64(e.c[j] * e.n)
			}
			cnt += float64(e.n)
		}
		if cnt == 0 {
			continue
		}
		pal = append(pal, color.RGBA{uint8(s[0]/cnt + 0.5), uint8(s[1]/cnt + 0.5), uint8(s[2]/cnt + 0.5), 255})
	}
	return pal
}

// dither maps img onto pal with Floyd-Steinberg error diffusion.
func dither(img *image.RGBA, pal color.Palette, cache []int16) *image.Paletted {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewPaletted(b, pal)
	cur := make([][3]float64, w+2)
	next := make([][3]float64, w+2)
	pc := make([][3]float64, len(pal))
	for i, c := range pal {
		r, g, bb, _ := c.RGBA()
		pc[i] = [3]float64{float64(r >> 8), float64(g >> 8), float64(bb >> 8)}
	}
	nearest := func(v [3]float64) int {
		var q [3]int
		for j := 0; j < 3; j++ {
			q[j] = int(math.Max(0, math.Min(255, v[j]))) >> 2
		}
		key := q[0]<<12 | q[1]<<6 | q[2]
		if cache[key] >= 0 {
			return int(cache[key])
		}
		best, bd := 0, math.MaxFloat64
		cv := [3]float64{float64(q[0]<<2 + 2), float64(q[1]<<2 + 2), float64(q[2]<<2 + 2)}
		for i, p := range pc {
			d := 3*sq(p[0]-cv[0]) + 4*sq(p[1]-cv[1]) + 2*sq(p[2]-cv[2])
			if d < bd {
				best, bd = i, d
			}
		}
		cache[key] = int16(best)
		return best
	}
	for y := 0; y < h; y++ {
		for x := range next {
			next[x] = [3]float64{}
		}
		for x := 0; x < w; x++ {
			o := img.PixOffset(x, y)
			var v [3]float64
			for j := 0; j < 3; j++ {
				v[j] = float64(img.Pix[o+j]) + math.Max(-24, math.Min(24, cur[x+1][j]))
			}
			idx := nearest(v)
			out.Pix[out.PixOffset(x, y)] = uint8(idx)
			for j := 0; j < 3; j++ {
				e := v[j] - pc[idx][j]
				cur[x+2][j] += e * 7 / 16
				next[x][j] += e * 3 / 16
				next[x+1][j] += e * 5 / 16
				next[x+2][j] += e * 1 / 16
			}
		}
		cur, next = next, cur
	}
	return out
}

func writeGIF(t *testing.T, path string, frames [][]byte, bg [3]uint8) {
	imgs := make([]*image.RGBA, len(frames))
	for i, f := range frames {
		imgs[i] = compose(f, bg)
	}
	var sample []*image.RGBA
	for i := 0; i < len(imgs); i += 3 {
		sample = append(sample, imgs[i])
	}
	pal := medianCut(sample, 256, bg)
	cache := make([]int16, 1<<18)
	for i := range cache {
		cache[i] = -1
	}
	g := &gif.GIF{LoopCount: 0}
	for i, im := range imgs {
		g.Image = append(g.Image, dither(im, pal, cache))
		g.Delay = append(g.Delay, []int{3, 3, 4}[i%3]) // 30 fps on average
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := gif.EncodeAll(f, g); err != nil {
		t.Fatal(err)
	}
	if sd := os.Getenv("VOICE_OVERLAY_STRIP"); sd != "" {
		// Every 4th frame at real size, to judge the motion.
		var cells [][]byte
		for i := 0; i < len(frames); i += 4 {
			cells = append(cells, frames[i])
		}
		name := filepath.Base(path)
		writeGrid(t, filepath.Join(sd, name[:len(name)-4]+"-strip.png"), cells, bg, 1, 10)
	}
}

func writeGrid(t *testing.T, path string, cells [][]byte, bg [3]uint8, scale, cols int) {
	cell, gap := Canvas*scale, 4*scale
	rows := (len(cells) + cols - 1) / cols
	img := image.NewRGBA(image.Rect(0, 0, cols*cell+(cols+1)*gap, rows*cell+(rows+1)*gap))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = bg[0], bg[1], bg[2], 255
	}
	for i, c := range cells {
		im := composeAt(c, bg, scale)
		x0, y0 := gap+(i%cols)*(cell+gap), gap+(i/cols)*(cell+gap)
		for y := 0; y < cell; y++ {
			copy(img.Pix[img.PixOffset(x0, y0+y):], im.Pix[im.PixOffset(0, y):im.PixOffset(cell, y)])
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, img)
}

// VOICE_OVERLAY_GIF=dir writes animated GIFs of every state and a still grid.
func TestWriteGIFs(t *testing.T) {
	dir := os.Getenv("VOICE_OVERLAY_GIF")
	if dir == "" {
		t.Skip("set VOICE_OVERLAY_GIF=dir")
	}
	os.MkdirAll(dir, 0o755)
	dark, light := [3]uint8{0x1e, 0x1e, 0x22}, [3]uint8{0xf3, 0xf3, 0xf3}
	only := os.Getenv("VOICE_OVERLAY_ONLY")
	want := func(n string) bool { return only == "" || only == n }

	if want("recording") {
		var sc scene
		film(&sc, recordingTL(6), -0.8, 0) // already open, still quiet
		writeGIF(t, filepath.Join(dir, "recording.gif"), film(&sc, recordingTL(6), 0, 6), dark)
	}
	if want("transcribing") {
		var sc scene
		tl := func(float64) State { return State{Transcribing: true} }
		film(&sc, tl, -1.5, 0)
		writeGIF(t, filepath.Join(dir, "transcribing.gif"), film(&sc, tl, 0, 3), dark)
	}
	if want("queued") {
		var sc scene
		tl := func(float64) State { return State{Queued: 3} }
		film(&sc, tl, -1.5, 0)
		writeGIF(t, filepath.Join(dir, "queued.gif"), film(&sc, tl, 0, 4), dark)
	}
	if want("deliver") {
		var sc scene
		q := func(float64) State { return State{Queued: 3} }
		film(&sc, q, -1.5, 0)
		fr := film(&sc, q, 0, 1.5)
		fr = append(fr, film(&sc, flashTL(State{}, Done, 1.5), 1.5, -1)...)
		writeGIF(t, filepath.Join(dir, "deliver.gif"), fr, dark)
	}
	if want("problem") {
		var sc scene
		tr := func(float64) State { return State{Transcribing: true} }
		film(&sc, tr, -1.5, 0)
		writeGIF(t, filepath.Join(dir, "problem.gif"), film(&sc, flashTL(State{}, Problem, 0), 0, 2.5), dark)
	}
	if want("sequence") {
		var sc scene
		writeGIF(t, filepath.Join(dir, "sequence.gif"), film(&sc, sequenceTL, 0, -1), dark)
		sc = scene{}
		writeGIF(t, filepath.Join(dir, "sequence-light.gif"), film(&sc, sequenceTL, 0, -1), light)
	}
	if want("still") {
		writeStill(t, filepath.Join(dir, "still.png"), dark)
	}
}

// writeStill is a 4x3 grid of representative frames.
func writeStill(t *testing.T, path string, bg [3]uint8) {
	grab := func(tl timeline, warm, t1 float64) []byte {
		var sc scene
		fr := film(&sc, tl, -warm, t1)
		return fr[len(fr)-1]
	}
	lvl := func(l float64) timeline { return func(float64) State { return State{Recording: true, Level: l} } }
	tr := func(float64) State { return State{Transcribing: true} }
	q := func(n int) timeline { return func(float64) State { return State{Queued: n} } }
	deliver := func(t float64) State {
		if t < 0 {
			return State{Queued: 3}
		}
		return State{Flash: Done, FlashUntil: at(1.6)}
	}
	problem := func(t float64) State {
		if t < 0 {
			return State{Transcribing: true}
		}
		return State{Flash: Problem, FlashUntil: at(1.6)}
	}
	cells := [][]byte{
		grab(lvl(0.05), 1.5, 0), grab(lvl(0.45), 1.5, 0), grab(lvl(1), 1.5, 0), grab(sequenceTL, 0, 4.55),
		grab(tr, 1.5, 0), grab(tr, 1.5, 0.4), grab(q(1), 1.5, 0), grab(q(3), 1.5, 0),
		grab(q(7), 1.5, 0), grab(deliver, 1.5, 0.3), grab(deliver, 1.5, 0.75), grab(problem, 1.5, 0.5),
	}
	writeGrid(t, path, cells, bg, 2, 4)
	if sd := os.Getenv("VOICE_OVERLAY_STRIP"); sd != "" {
		writeGrid(t, filepath.Join(sd, "still-1x.png"), cells, bg, 1, 4)
		writeGrid(t, filepath.Join(sd, "still-light-1x.png"), cells, [3]uint8{0xf3, 0xf3, 0xf3}, 1, 4)
	}
}
