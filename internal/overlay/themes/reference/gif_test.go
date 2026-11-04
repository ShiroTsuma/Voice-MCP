package reference

import (
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// VOICE_OVERLAY_GIF=dir writes animated GIFs of every state and a still of
// representative frames into dir, to look at. They are composited on a dark
// desktop (#1e1e22), sequence-light.gif on a light one (#f3f3f3), and shown
// at twice the real size.
func TestWriteGIFs(t *testing.T) {
	dir := os.Getenv("VOICE_OVERLAY_GIF")
	if dir == "" {
		t.Skip("set VOICE_OVERLAY_GIF=dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dark := color.RGBA{0x1e, 0x1e, 0x22, 255}
	light := color.RGBA{0xf3, 0xf3, 0xf3, 255}

	// A speech-like envelope over d seconds: quiet, loud in the middle with
	// syllables, quiet again.
	speech := func(t, d float64) float64 {
		shape := math.Pow(math.Sin(math.Pi*t/d), 1.5)
		syl := 0.55 + 0.45*math.Abs(math.Sin(t*9.5+1))*(0.7+0.3*math.Sin(t*2.3))
		return clamp(shape * syl * 1.05)
	}
	rec := func(t, d float64) (Kind, State) {
		return Recording, State{Recording: true, Level: speech(t, d)}
	}

	type clip struct {
		name string
		bg   color.RGBA
		dur  float64
		at   func(t float64) (Kind, State) // nil kind: let the scene run on from the last state
	}
	seq := func(t float64) (Kind, State) {
		switch {
		case t < 4:
			return rec(t, 4)
		case t < 6:
			return Transcribing, State{Transcribing: true}
		case t < 8:
			return Queued, State{Queued: 2}
		case t < 9.6:
			return Done, State{}
		}
		return Hidden, State{}
	}
	clips := []clip{
		{"recording", dark, 6, func(t float64) (Kind, State) { return rec(t, 6) }},
		{"transcribing", dark, 3, func(t float64) (Kind, State) { return Transcribing, State{Transcribing: true} }},
		{"queued", dark, 4, func(t float64) (Kind, State) { return Queued, State{Queued: 3} }},
		{"deliver", dark, 4, func(t float64) (Kind, State) {
			switch {
			case t < 1.5:
				return Queued, State{Queued: 3}
			case t < 3.1:
				return Done, State{}
			}
			return Hidden, State{}
		}},
		{"problem", dark, 2.5, func(t float64) (Kind, State) { return Problem, State{} }},
		{"sequence", dark, 11, seq},
		{"sequence-light", light, 11, seq},
	}
	for _, c := range clips {
		var sc scene
		var frames []*image.RGBA
		for i := 0; float64(i)/30 < c.dur; i++ {
			t := float64(i) / 30
			k, s := c.at(t)
			sc.step(k, s, t, 1.0/30)
			frames = append(frames, composite(sc.render(), c.bg))
		}
		writeGIF(t, filepath.Join(dir, c.name+".gif"), frames)
	}

	// A still: 3 rows by 4 of representative frames, at twice the size and
	// (still-1x.png) at the real size.
	var cells, cells1x []*image.RGBA
	grab := func(sc *scene) {
		px := sc.render()
		cells = append(cells, composite(px, dark))
		cells1x = append(cells1x, flatten(px, dark))
	}
	{
		var sc scene
		settle(&sc, Recording, State{Recording: true, Level: 0}, 0, 0.25) // appearing
		grab(&sc)
		settle(&sc, Recording, State{Recording: true, Level: 0.05}, 0.25, 2)
		grab(&sc)
		settle(&sc, Recording, State{Recording: true, Level: 0.55}, 2.25, 0.5)
		grab(&sc)
		settle(&sc, Recording, State{Recording: true, Level: 1}, 2.75, 0.4)
		grab(&sc)
		settle(&sc, Transcribing, State{Transcribing: true}, 3.15, 1.4)
		grab(&sc)
		settle(&sc, Queued, State{Queued: 1}, 4.55, 2)
		grab(&sc)
		settle(&sc, Queued, State{Queued: 3}, 6.55, 2)
		grab(&sc)
		settle(&sc, Queued, State{Queued: 5}, 8.55, 2)
		grab(&sc)
		settle(&sc, Done, State{}, 10.55, 0.3)
		grab(&sc)
		settle(&sc, Done, State{}, 10.85, 0.4)
		grab(&sc)
		settle(&sc, Problem, State{}, 11.25, 0.5)
		grab(&sc)
		settle(&sc, Hidden, State{}, 11.75, 0.35) // vanishing
		grab(&sc)
	}
	for name, cs := range map[string][]*image.RGBA{"still.png": cells, "still-1x.png": cells1x} {
		const cols, rows, gap = 4, 3, 8
		cell := cs[0].Bounds().Dx()
		still := image.NewRGBA(image.Rect(0, 0, cols*cell+(cols+1)*gap, rows*cell+(rows+1)*gap))
		draw.Draw(still, still.Bounds(), image.NewUniform(dark), image.Point{}, draw.Src)
		for i, c := range cs {
			x, y := gap+(i%cols)*(cell+gap), gap+(i/cols)*(cell+gap)
			draw.Draw(still, image.Rect(x, y, x+cell, y+cell), c, image.Point{}, draw.Src)
		}
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, still); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}

// flatten draws a premultiplied BGRA canvas over bg at its real size.
func flatten(px []byte, bg color.RGBA) *image.RGBA {
	const c = Canvas
	img := image.NewRGBA(image.Rect(0, 0, c, c))
	bgc := [3]float64{float64(bg.R), float64(bg.G), float64(bg.B)}
	for i := 0; i < c*c; i++ {
		a := float64(px[i*4+3]) / 255
		for j, off := range [3]int{2, 1, 0} {
			img.Pix[i*4+j] = uint8(float64(px[i*4+off]) + bgc[j]*(1-a) + 0.5)
		}
		img.Pix[i*4+3] = 255
	}
	return img
}

// composite draws a premultiplied BGRA canvas over bg and doubles it with a
// smooth (bilinear) upscale.
func composite(px []byte, bg color.RGBA) *image.RGBA {
	const c = Canvas
	flat := make([][3]float64, c*c)
	bgc := [3]float64{float64(bg.R), float64(bg.G), float64(bg.B)}
	for i := range flat {
		a := float64(px[i*4+3]) / 255
		for j, off := range [3]int{2, 1, 0} {
			flat[i][j] = float64(px[i*4+off]) + bgc[j]*(1-a)
		}
	}
	at := func(x, y int) [3]float64 {
		return flat[min(max(y, 0), c-1)*c+min(max(x, 0), c-1)]
	}
	img := image.NewRGBA(image.Rect(0, 0, 2*c, 2*c))
	for y := 0; y < 2*c; y++ {
		for x := 0; x < 2*c; x++ {
			// Sample positions ±0.25 from the source pixel centre.
			sx, sy := (float64(x)+0.5)/2-0.5, (float64(y)+0.5)/2-0.5
			x0, y0 := int(math.Floor(sx)), int(math.Floor(sy))
			fx, fy := sx-float64(x0), sy-float64(y0)
			var v [3]float64
			for j := range v {
				v[j] = at(x0, y0)[j]*(1-fx)*(1-fy) + at(x0+1, y0)[j]*fx*(1-fy) +
					at(x0, y0+1)[j]*(1-fx)*fy + at(x0+1, y0+1)[j]*fx*fy
			}
			o := img.PixOffset(x, y)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] =
				uint8(v[0]+0.5), uint8(v[1]+0.5), uint8(v[2]+0.5), 255
		}
	}
	return img
}

// writeGIF quantizes the frames with one palette built from them (median
// cut, no dithering: a dithered halo would shimmer from frame to frame) and
// writes them at 30 fps, looping.
func writeGIF(t *testing.T, path string, frames []*image.RGBA) {
	var samples [][3]uint8
	for fi, f := range frames {
		if fi%2 != 0 {
			continue
		}
		for i := 0; i < len(f.Pix); i += 4 * 3 {
			samples = append(samples, [3]uint8{f.Pix[i], f.Pix[i+1], f.Pix[i+2]})
		}
	}
	pal := medianCut(samples, 256)
	g := &gif.GIF{LoopCount: 0}
	for _, f := range frames {
		pm := image.NewPaletted(f.Bounds(), pal)
		draw.Draw(pm, pm.Bounds(), f, image.Point{}, draw.Src)
		g.Image = append(g.Image, pm)
		g.Delay = append(g.Delay, 3)
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
}

// medianCut builds a palette of up to n colours from samples: the box of all
// colours is split at the median of its longest side, biggest box first,
// until there are n boxes, and each box's mean is a palette entry.
func medianCut(samples [][3]uint8, n int) color.Palette {
	type box struct {
		px         [][3]uint8
		lo, hi     [3]int
		axis, span int
	}
	measure := func(b *box) {
		b.lo, b.hi = [3]int{255, 255, 255}, [3]int{0, 0, 0}
		for _, p := range b.px {
			for j := range p {
				b.lo[j], b.hi[j] = min(b.lo[j], int(p[j])), max(b.hi[j], int(p[j]))
			}
		}
		b.axis, b.span = 0, -1
		for j := range b.lo {
			if s := b.hi[j] - b.lo[j]; s > b.span {
				b.axis, b.span = j, s
			}
		}
	}
	boxes := []*box{{px: samples}}
	measure(boxes[0])
	for len(boxes) < n {
		// Split the box with the largest spread (weighted a little by size).
		best, bestScore := -1, 0.0
		for i, b := range boxes {
			if b.span == 0 || len(b.px) < 2 {
				continue
			}
			if score := float64(b.span) * math.Sqrt(float64(len(b.px))); score > bestScore {
				best, bestScore = i, score
			}
		}
		if best < 0 {
			break
		}
		b := boxes[best]
		ax := b.axis
		sort.Slice(b.px, func(i, j int) bool { return b.px[i][ax] < b.px[j][ax] })
		m := len(b.px) / 2
		nb := &box{px: b.px[m:]}
		b.px = b.px[:m]
		measure(b)
		measure(nb)
		boxes = append(boxes, nb)
	}
	pal := make(color.Palette, 0, len(boxes))
	for _, b := range boxes {
		var s [3]float64
		for _, p := range b.px {
			for j := range p {
				s[j] += float64(p[j])
			}
		}
		k := float64(len(b.px))
		pal = append(pal, color.RGBA{uint8(s[0]/k + 0.5), uint8(s[1]/k + 0.5), uint8(s[2]/k + 0.5), 255})
	}
	return pal
}
