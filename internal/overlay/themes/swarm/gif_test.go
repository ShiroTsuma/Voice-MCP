package swarm

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
)

// A clip is a list of scenes to play one after the other, each for a while or,
// with a negative duration, until the swarm has faded away.
type beat struct {
	kind  Kind
	state func(t float64) State // t: seconds into this beat
	dur   float64
}

func still(s State) func(float64) State { return func(float64) State { return s } }

// speech is a speech-like microphone envelope over d seconds: quiet, loud,
// quiet, with syllables.
func speech(d float64) func(float64) State {
	return func(t float64) State {
		hull := math.Pow(math.Sin(math.Pi*t/d), 1.3)
		syl := 0.5 + 0.5*math.Abs(math.Sin(t*7.3))*(0.55+0.45*math.Sin(t*2.1+1))
		burst := 0.15 * math.Max(0, math.Sin(t*23.0))
		return State{Recording: true, Level: clamp(hull*(0.15+0.95*syl) + burst*hull)}
	}
}

// play runs a clip at 30 fps and returns each frame, native size, premultiplied.
func play(beats []beat) [][]byte {
	var sc scene
	var frames [][]byte
	const dt = 1.0 / 30
	t := 0.0
	for _, b := range beats {
		for u := 0.0; b.dur < 0 || u < b.dur; u += dt {
			visible := sc.step(b.kind, b.state(u), t, dt)
			t += dt
			frames = append(frames, sc.render())
			if !visible && b.dur < 0 {
				break
			}
		}
	}
	return frames
}

// over composites a premultiplied frame on a background and scales it 2x,
// bilinearly.
func over(px []byte, bg color.RGBA) *image.RGBA {
	const out = Canvas * 2
	src := make([]float64, Canvas*Canvas*3)
	for i := 0; i < Canvas*Canvas; i++ {
		a := float64(px[i*4+3]) / 255
		src[i*3] = float64(px[i*4+2]) + (1-a)*float64(bg.R)
		src[i*3+1] = float64(px[i*4+1]) + (1-a)*float64(bg.G)
		src[i*3+2] = float64(px[i*4]) + (1-a)*float64(bg.B)
	}
	at := func(x, y, c int) float64 {
		x, y = max(0, min(Canvas-1, x)), max(0, min(Canvas-1, y))
		return src[(y*Canvas+x)*3+c]
	}
	img := image.NewRGBA(image.Rect(0, 0, out, out))
	for y := 0; y < out; y++ {
		fy := (float64(y)+0.5)/2 - 0.5
		y0 := int(math.Floor(fy))
		ty := fy - float64(y0)
		for x := 0; x < out; x++ {
			fx := (float64(x)+0.5)/2 - 0.5
			x0 := int(math.Floor(fx))
			tx := fx - float64(x0)
			o := img.PixOffset(x, y)
			for c := 0; c < 3; c++ {
				v := (at(x0, y0, c)*(1-tx)+at(x0+1, y0, c)*tx)*(1-ty) +
					(at(x0, y0+1, c)*(1-tx)+at(x0+1, y0+1, c)*tx)*ty
				img.Pix[o+c] = uint8(v + 0.5)
			}
			img.Pix[o+3] = 255
		}
	}
	return img
}

// palette picks up to n colours for the frames by median cut.
func palette(frames []*image.RGBA, n int) color.Palette {
	var px [][3]uint8
	for f, img := range frames {
		if f%2 != 0 {
			continue
		}
		for i := 0; i+3 < len(img.Pix); i += 4 * 3 {
			px = append(px, [3]uint8{img.Pix[i], img.Pix[i+1], img.Pix[i+2]})
		}
	}
	boxes := [][][3]uint8{px}
	for len(boxes) < n {
		// Split the box with the widest channel range.
		bi, bc, br := -1, 0, -1
		for i, b := range boxes {
			if len(b) < 2 {
				continue
			}
			var lo, hi [3]int
			lo = [3]int{255, 255, 255}
			for _, p := range b {
				for c := range p {
					lo[c], hi[c] = min(lo[c], int(p[c])), max(hi[c], int(p[c]))
				}
			}
			for c := 0; c < 3; c++ {
				if r := hi[c] - lo[c]; r > br {
					bi, bc, br = i, c, r
				}
			}
		}
		if bi < 0 || br == 0 {
			break
		}
		b := boxes[bi]
		sort.Slice(b, func(i, j int) bool { return b[i][bc] < b[j][bc] })
		m := len(b) / 2
		boxes[bi] = b[:m]
		boxes = append(boxes, b[m:])
	}
	pal := make(color.Palette, 0, len(boxes))
	for _, b := range boxes {
		var s [3]int
		for _, p := range b {
			for c := range p {
				s[c] += int(p[c])
			}
		}
		pal = append(pal, color.RGBA{uint8(s[0] / len(b)), uint8(s[1] / len(b)), uint8(s[2] / len(b)), 255})
	}
	return pal
}

// quantize maps a frame onto the palette with a small ordered dither, which
// hides banding without the crawling noise an error-diffusing dither makes
// between frames.
func quantize(img *image.RGBA, pal color.Palette, cache map[uint32]uint8) *image.Paletted {
	bayer := [4][4]float64{{0, 8, 2, 10}, {12, 4, 14, 6}, {3, 11, 1, 9}, {15, 7, 13, 5}}
	out := image.NewPaletted(img.Bounds(), pal)
	w := img.Bounds().Dx()
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < w; x++ {
			o := img.PixOffset(x, y)
			d := (bayer[y&3][x&3]/16 - 0.5) * 3
			var c [3]uint8
			for i := range c {
				c[i] = uint8(clamp((float64(img.Pix[o+i])+d)/255) * 255)
			}
			key := uint32(c[0])<<16 | uint32(c[1])<<8 | uint32(c[2])
			idx, ok := cache[key]
			if !ok {
				idx = uint8(pal.Index(color.RGBA{c[0], c[1], c[2], 255}))
				cache[key] = idx
			}
			out.Pix[y*w+x] = idx
		}
	}
	return out
}

func writeGIF(t *testing.T, path string, frames [][]byte, bg color.RGBA) {
	imgs := make([]*image.RGBA, len(frames))
	for i, f := range frames {
		imgs[i] = over(f, bg)
	}
	pal := palette(imgs, 255)
	cache := map[uint32]uint8{}
	g := &gif.GIF{LoopCount: 0}
	for _, img := range imgs {
		g.Image = append(g.Image, quantize(img, pal, cache))
		g.Delay = append(g.Delay, 3) // hundredths: ~30 fps
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
	t.Logf("%s: %d frames", filepath.Base(path), len(frames))
}

var (
	darkBG  = color.RGBA{0x1e, 0x1e, 0x22, 255}
	lightBG = color.RGBA{0xf3, 0xf3, 0xf3, 255}
)

// VOICE_OVERLAY_GIF=dir writes animated GIFs of every state and a contact
// sheet, to look at.
func TestWriteGIFs(t *testing.T) {
	dir := os.Getenv("VOICE_OVERLAY_GIF")
	if dir == "" {
		t.Skip("set VOICE_OVERLAY_GIF=dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	queued3 := still(State{Queued: 3})
	sequence := []beat{
		{Hidden, still(State{}), 0.4},
		{Recording, speech(4), 4},
		{Transcribing, still(State{Transcribing: true}), 2},
		{Queued, still(State{Queued: 2}), 2},
		{Done, still(State{}), 1.6},
		{Hidden, still(State{}), -1},
	}
	clips := []struct {
		name  string
		beats []beat
		bg    color.RGBA
	}{
		{"recording", []beat{{Recording, speech(6), 6}}, darkBG},
		{"transcribing", []beat{{Transcribing, still(State{Transcribing: true}), 3}}, darkBG},
		{"queued", []beat{{Queued, queued3, 4}}, darkBG},
		{"deliver", []beat{{Queued, queued3, 1.5}, {Done, still(State{}), 1.6}, {Hidden, still(State{}), -1}}, darkBG},
		{"problem", []beat{{Problem, still(State{}), 2.5}}, darkBG},
		{"sequence", sequence, darkBG},
		{"sequence-light", sequence, lightBG},
	}
	for _, c := range clips {
		writeGIF(t, filepath.Join(dir, c.name+".gif"), play(c.beats), c.bg)
	}

	// A contact sheet: 4 columns by 3 rows of representative moments.
	type shot struct {
		beats []beat
		at    int // frame to keep
	}
	rec := func(l float64) []beat { return []beat{{Recording, still(State{Recording: true, Level: l}), 2}} }
	shots := []shot{
		{[]beat{{Recording, still(State{Recording: true, Level: 0.3}), 0.25}}, 7}, // flying in
		{rec(0.05), 55},
		{rec(0.95), 55},
		{[]beat{{Transcribing, still(State{Transcribing: true}), 2}}, 50},
		{[]beat{{Queued, still(State{Queued: 1}), 2}}, 50},
		{[]beat{{Queued, still(State{Queued: 3}), 2}}, 50},
		{[]beat{{Queued, still(State{Queued: 7}), 2}}, 50},
		{[]beat{{Queued, queued3, 1.5}, {Done, still(State{}), 1}}, 45 + 8},
		{[]beat{{Queued, queued3, 1.5}, {Done, still(State{}), 1}}, 45 + 20},
		{[]beat{{Done, still(State{}), 1.6}}, 40},
		{[]beat{{Transcribing, still(State{Transcribing: true}), 1}, {Problem, still(State{}), 1}}, 30 + 9},
		{[]beat{{Problem, still(State{}), 1}, {Hidden, still(State{}), 1}}, 30 + 8}, // fading away
	}
	const cols, rows = 4, 3
	sheet := image.NewRGBA(image.Rect(0, 0, cols*Canvas*2, rows*Canvas*2))
	light := image.NewRGBA(sheet.Bounds())
	for i, s := range shots {
		frames := play(s.beats)
		frame := frames[min(s.at, len(frames)-1)]
		ox, oy := (i%cols)*Canvas*2, (i/cols)*Canvas*2
		for _, v := range []struct {
			dst *image.RGBA
			bg  color.RGBA
		}{{sheet, darkBG}, {light, lightBG}} {
			img := over(frame, v.bg)
			for y := 0; y < Canvas*2; y++ {
				copy(v.dst.Pix[v.dst.PixOffset(ox, oy+y):], img.Pix[img.PixOffset(0, y):img.PixOffset(0, y)+Canvas*2*4])
			}
		}
	}
	writePNG(t, filepath.Join(dir, "still.png"), sheet)
	writePNG(t, filepath.Join(dir, "still-light.png"), light)

	// A filmstrip of consecutive frames, to check that motion is smooth. Not
	// a deliverable: it goes next to the GIFs only when asked for.
	if os.Getenv("VOICE_OVERLAY_STRIP") == "" {
		return
	}
	frames := play(sequence)
	const n = 10
	strip := image.NewRGBA(image.Rect(0, 0, n*Canvas*2, 3*Canvas*2))
	for row, start := range []int{60, 190, 265} {
		for i := 0; i < n; i++ {
			img := over(frames[start+i], darkBG)
			ox, oy := i*Canvas*2, row*Canvas*2
			for y := 0; y < Canvas*2; y++ {
				copy(strip.Pix[strip.PixOffset(ox, oy+y):], img.Pix[img.PixOffset(0, y):img.PixOffset(0, y)+Canvas*2*4])
			}
		}
	}
	writePNG(t, filepath.Join(dir, "strip.png"), strip)
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
