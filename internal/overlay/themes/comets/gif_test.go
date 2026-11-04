package comets

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

// VOICE_OVERLAY_GIF=dir writes animated GIFs of every state (30 fps, 2x,
// composited on a dark and a light desktop) and a still of frames, to look at.
func TestWriteGIFs(t *testing.T) {
	dir := os.Getenv("VOICE_OVERLAY_GIF")
	if dir == "" {
		t.Skip("set VOICE_OVERLAY_GIF=dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dark := color.RGBA{0x1e, 0x1e, 0x22, 0xff}
	light := color.RGBA{0xf3, 0xf3, 0xf3, 0xff}

	rec := func(t float64) (Kind, State) { return Recording, State{Recording: true, Level: speech(t, 6)} }
	tra := func(float64) (Kind, State) { return Transcribing, State{Transcribing: true} }
	que := func(float64) (Kind, State) { return Queued, State{Queued: 3} }
	deliver := func(t float64) (Kind, State) {
		if t < 1.5 {
			return Queued, State{Queued: 3}
		}
		if t < 3.1 {
			return Done, State{}
		}
		return Hidden, State{}
	}
	problem := func(t float64) (Kind, State) {
		if t < 1.8 {
			return Problem, State{}
		}
		return Hidden, State{}
	}
	sequence := func(t float64) (Kind, State) {
		switch {
		case t < 4:
			return Recording, State{Recording: true, Level: speech(t, 4)}
		case t < 6:
			return Transcribing, State{Transcribing: true}
		case t < 8:
			return Queued, State{Queued: 2}
		case t < 9.6:
			return Done, State{}
		}
		return Hidden, State{}
	}
	clips := []struct {
		name string
		bg   color.RGBA
		secs float64
		play func(t float64) (Kind, State)
	}{
		{"recording", dark, 6, rec},
		{"transcribing", dark, 3, tra},
		{"queued", dark, 4, que},
		{"deliver", dark, 4, deliver},
		{"problem", dark, 2.5, problem},
		{"sequence", dark, 11, sequence},
		{"sequence-light", light, 11, sequence},
	}
	for _, c := range clips {
		frames := play(c.play, c.secs, c.bg)
		if err := writeGIF(filepath.Join(dir, c.name+".gif"), frames, c.bg); err != nil {
			t.Fatal(err)
		}
	}
	writeStill(t, filepath.Join(dir, "still.png"), dark)
	writeStill(t, filepath.Join(dir, "still-light.png"), light)
}

// speech is a speech-like level envelope over d seconds: quiet, then
// syllables rising to loud, then quiet again.
func speech(t, d float64) float64 {
	u := t / d
	macro := math.Pow(math.Sin(math.Pi*u), 1.4) // quiet, loud, quiet
	syl := 0.55 + 0.45*math.Sin(2*math.Pi*3.4*t)*math.Sin(2*math.Pi*1.3*t+1)
	pause := 0.35 + 0.65*smoothstep(-0.2, 0.3, math.Sin(2*math.Pi*0.7*t+2))
	return clamp(macro * syl * pause * 1.25)
}

func smoothstep(e0, e1, v float64) float64 {
	t := clamp((v - e0) / (e1 - e0))
	return t * t * (3 - 2*t)
}

// play runs the scene through a clip at 30 fps and returns each frame
// composited on bg, upscaled 2x.
func play(clip func(t float64) (Kind, State), secs float64, bg color.RGBA) []*image.RGBA {
	var sc scene
	var frames []*image.RGBA
	const dt = 1.0 / 30
	for t := 0.0; t < secs; t += dt {
		k, s := clip(t)
		sc.step(k, s, t, dt)
		frames = append(frames, upscale(sc.render(), bg))
	}
	return frames
}

// upscale composites premultiplied BGRA on bg and doubles it, bilinearly.
func upscale(px []byte, bg color.RGBA) *image.RGBA {
	src := make([][3]float64, Canvas*Canvas)
	b := [3]float64{float64(bg.R), float64(bg.G), float64(bg.B)}
	for i := range src {
		a := float64(px[i*4+3]) / 255
		src[i] = [3]float64{
			float64(px[i*4+2]) + b[0]*(1-a),
			float64(px[i*4+1]) + b[1]*(1-a),
			float64(px[i*4]) + b[2]*(1-a),
		}
	}
	const W = Canvas * 2
	img := image.NewRGBA(image.Rect(0, 0, W, W))
	at := func(x, y int) [3]float64 {
		return src[min(max(y, 0), Canvas-1)*Canvas+min(max(x, 0), Canvas-1)]
	}
	for y := 0; y < W; y++ {
		fy := (float64(y)+0.5)/2 - 0.5
		y0 := int(math.Floor(fy))
		wy := fy - float64(y0)
		for x := 0; x < W; x++ {
			fx := (float64(x)+0.5)/2 - 0.5
			x0 := int(math.Floor(fx))
			wx := fx - float64(x0)
			// A fixed sub-pixel dither keeps the soft gradients from banding
			// once rounded and quantized, without any shimmer between frames.
			n := float64((x*7+y*13)%9)/9 - 0.44
			var c [3]float64
			for j := range c {
				c[j] = (at(x0, y0)[j]*(1-wx)+at(x0+1, y0)[j]*wx)*(1-wy) +
					(at(x0, y0+1)[j]*(1-wx)+at(x0+1, y0+1)[j]*wx)*wy
				c[j] = math.Max(0, math.Min(255, c[j]+n))
			}
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(c[0]+0.5), byte(c[1]+0.5), byte(c[2]+0.5), 255
		}
	}
	return img
}

// writeGIF encodes frames at 30 fps with one palette fitted to all of them.
func writeGIF(path string, frames []*image.RGBA, bg color.RGBA) error {
	pal := fitPalette(frames, bg)
	cache := map[uint32]uint8{}
	nearest := func(r, g, b byte) uint8 {
		key := uint32(r)<<16 | uint32(g)<<8 | uint32(b)
		if i, ok := cache[key]; ok {
			return i
		}
		best, bestD := 0, math.MaxInt
		for i, c := range pal {
			pc := c.(color.RGBA)
			dr, dg, db := int(pc.R)-int(r), int(pc.G)-int(g), int(pc.B)-int(b)
			if d := dr*dr + dg*dg + db*db; d < bestD {
				best, bestD = i, d
			}
		}
		cache[key] = uint8(best)
		return uint8(best)
	}
	out := &gif.GIF{LoopCount: 0}
	out.Config = image.Config{ColorModel: pal, Width: frames[0].Rect.Dx(), Height: frames[0].Rect.Dy()}
	for n, f := range frames {
		p := image.NewPaletted(f.Rect, pal)
		for i := 0; i < len(f.Pix); i += 4 {
			p.Pix[i/4] = nearest(f.Pix[i], f.Pix[i+1], f.Pix[i+2])
		}
		out.Image = append(out.Image, p)
		delay := 3 // 3, 3, 4 hundredths: 30 frames a second
		if n%3 == 2 {
			delay = 4
		}
		out.Delay = append(out.Delay, delay)
		out.Disposal = append(out.Disposal, gif.DisposalNone)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return gif.EncodeAll(f, out)
}

// fitPalette picks 256 colours for the frames by median cut, keeping the
// background exact so it never shifts.
func fitPalette(frames []*image.RGBA, bg color.RGBA) color.Palette {
	var samples []uint32
	bgKey := uint32(bg.R)<<16 | uint32(bg.G)<<8 | uint32(bg.B)
	for n, f := range frames {
		if n%2 == 1 && len(frames) > 60 {
			continue
		}
		for i := 0; i < len(f.Pix); i += 4 {
			key := uint32(f.Pix[i])<<16 | uint32(f.Pix[i+1])<<8 | uint32(f.Pix[i+2])
			if key != bgKey {
				samples = append(samples, key)
			}
		}
	}
	pal := color.Palette{bg}
	if len(samples) == 0 {
		return pal
	}
	boxes := [][]uint32{samples}
	ch := func(c uint32, j int) int { return int(c>>(16-8*uint(j))) & 0xff }
	for len(boxes) < 255 {
		// Split the box with the widest side, at its median.
		bi, bw, bc := -1, 0, 0
		for i, b := range boxes {
			if len(b) < 2 {
				continue
			}
			lo, hi := [3]int{255, 255, 255}, [3]int{}
			for _, c := range b {
				for j := range lo {
					v := ch(c, j)
					lo[j], hi[j] = min(lo[j], v), max(hi[j], v)
				}
			}
			for j := range lo {
				if w := hi[j] - lo[j]; w > bw || (w == bw && len(b) > len(boxes[max(bi, 0)])) {
					bi, bw, bc = i, w, j
				}
			}
		}
		if bi < 0 || bw == 0 {
			break
		}
		b := boxes[bi]
		sort.Slice(b, func(x, y int) bool { return ch(b[x], bc) < ch(b[y], bc) })
		mid := len(b) / 2
		boxes[bi] = b[:mid]
		boxes = append(boxes, b[mid:])
	}
	for _, b := range boxes {
		var sum [3]int
		for _, c := range b {
			for j := range sum {
				sum[j] += ch(c, j)
			}
		}
		n := len(b)
		pal = append(pal, color.RGBA{uint8(sum[0] / n), uint8(sum[1] / n), uint8(sum[2] / n), 255})
	}
	return pal
}

// writeStill lays representative frames out in a 4x3 grid with labels.
func writeStill(t *testing.T, path string, bg color.RGBA) {
	type shot struct {
		label string
		k     Kind
		s     State
		secs  float64 // settle time in that state
		pre   func(sc *scene)
	}
	shots := []shot{
		{"APPEARING", Recording, State{Recording: true, Level: 0.2}, 0.12, nil},
		{"RECORDING QUIET", Recording, State{Recording: true, Level: 0.1}, 2, nil},
		{"RECORDING LOUD", Recording, State{Recording: true, Level: 0.85}, 2, nil},
		{"TRANSCRIBING", Transcribing, State{Transcribing: true}, 2, nil},
		{"QUEUED 1", Queued, State{Queued: 1}, 2, nil},
		{"QUEUED 3", Queued, State{Queued: 3}, 2, nil},
		{"QUEUED 5", Queued, State{Queued: 5}, 2, nil},
		{"QUEUED 7", Queued, State{Queued: 7}, 2, nil},
		{"DONE MERGING", Done, State{}, 0.25, func(sc *scene) { settle(sc, Queued, State{Queued: 3}, 0, 2) }},
		{"DONE BLOOM", Done, State{}, 0.5, func(sc *scene) { settle(sc, Queued, State{Queued: 3}, 0, 2) }},
		{"PROBLEM", Problem, State{}, 0.8, nil},
		{"LEAVING", Hidden, State{}, 0.15, func(sc *scene) { settle(sc, Queued, State{Queued: 2}, 0, 2) }},
	}
	const cell, gap, cols = Canvas * 2, 12, 4
	rows := (len(shots) + cols - 1) / cols
	out := image.NewRGBA(image.Rect(0, 0, cols*cell+(cols+1)*gap, rows*(cell+16)+(rows+1)*gap))
	for i := range out.Pix {
		out.Pix[i] = [...]byte{bg.R, bg.G, bg.B, 255}[i%4]
	}
	for n, sh := range shots {
		var sc scene
		if sh.pre != nil {
			sh.pre(&sc)
		}
		settle(&sc, sh.k, sh.s, 2, sh.secs)
		frame := upscale(sc.render(), bg)
		x0 := gap + (n%cols)*(cell+gap)
		y0 := gap + (n/cols)*(cell+16+gap)
		for y := 0; y < cell; y++ {
			copy(out.Pix[out.PixOffset(x0, y0+y):out.PixOffset(x0+cell, y0+y)], frame.Pix[frame.PixOffset(0, y):frame.PixOffset(cell, y)])
		}
		ink := color.RGBA{0x9a, 0x9e, 0xa6, 255}
		if int(bg.R)+int(bg.G)+int(bg.B) > 384 {
			ink = color.RGBA{0x6a, 0x6e, 0x76, 255}
		}
		label(out, x0+2, y0+cell+4, sh.label, ink)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, out); err != nil {
		t.Fatal(err)
	}
}

// glyphs is a 3x5 pixel font, five rows of three bits per character.
var glyphs = map[rune]string{
	'A': "010101111101101", 'B': "110101110101110", 'C': "011100100100011", 'D': "110101101101110",
	'E': "111100110100111", 'F': "111100110100100", 'G': "011100101101011", 'H': "101101111101101",
	'I': "111010010010111", 'J': "001001001101010", 'K': "101101110101101", 'L': "100100100100111",
	'M': "101111111101101", 'N': "110101101101101", 'O': "010101101101010", 'P': "110101110100100",
	'Q': "010101101111011", 'R': "110101110101101", 'S': "011100010001110", 'T': "111010010010010",
	'U': "101101101101111", 'V': "101101101101010", 'W': "101101111111101", 'X': "101101010101101",
	'Y': "101101010010010", 'Z': "111001010100111", '0': "111101101101111", '1': "010110010010111",
	'2': "111001111100111", '3': "111001111001111", '4': "101101111001001", '5': "111100111001111",
	'6': "111100111101111", '7': "111001001001001", '8': "111101111101111", '9': "111101111001111",
	'.': "000000000000010", '-': "000000111000000", ' ': "000000000000000",
}

func label(img *image.RGBA, x, y int, s string, c color.RGBA) {
	for _, r := range s {
		g, ok := glyphs[r]
		if !ok {
			g = glyphs[' ']
		}
		for i, b := range g {
			if b == '1' {
				px, py := x+(i%3)*2, y+(i/3)*2
				for dy := 0; dy < 2; dy++ {
					for dx := 0; dx < 2; dx++ {
						img.SetRGBA(px+dx, py+dy, c)
					}
				}
			}
		}
		x += 8
	}
}
