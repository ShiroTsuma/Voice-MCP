package waveform

import "math"

// Rendering: every particle is splatted as a small gaussian with a white-hot
// core into a linear light buffer; a blurred copy of that buffer is added back
// as glow; the light is tone mapped so dense spots burn to white instead of
// clipping, and composited over a soft dark halo that keeps it readable on a
// light desktop. The result is premultiplied BGRA, top row first.

const (
	glowRadius = 4    // box blur radius, three passes: about a 4.5 px gaussian
	glowGain   = 0.9  // how much of the blurred light comes back
	pearlSigma = 2.3  // pearls are bigger than dots
	alphaBoost = 1.2  // light's alpha relative to its brightest channel
	haloInner  = 30.0 // the halo is full inside this radius...
	haloOuter  = 62.0 // ...and gone outside this one
)

var haloCol = rgb{0.03, 0.05, 0.09}

// toneLUT maps linear light (0..8) to 1-exp(-v).
var toneLUT = func() [2049]float32 {
	var lut [2049]float32
	for i := range lut {
		lut[i] = float32(1 - math.Exp(-float64(i)*8/2048))
	}
	return lut
}()

func tone(v float32) float32 {
	if v <= 0 {
		return 0
	}
	if v >= 8 {
		return 1
	}
	return toneLUT[int(v*256)]
}

func (sc *scene) alloc() {
	n := Canvas * Canvas
	sc.light = make([]float32, n*3)
	sc.glow = make([]float32, n*3)
	sc.tmp = make([]float32, n*3)
	sc.out = make([]byte, n*4)
	sc.haloMask = make([]float32, n)
	for y := 0; y < Canvas; y++ {
		for x := 0; x < Canvas; x++ {
			r := math.Hypot(float64(x)+0.5-centre, float64(y)+0.5-centre)
			sc.haloMask[y*Canvas+x] = float32(1 - smooth(haloInner, haloOuter, r))
		}
	}
}

// splat adds one gaussian particle of colour c and brightness b at (cx, cy),
// with a whiter, tighter core.
func (sc *scene) splat(cx, cy, sigma, b float64, c rgb) {
	rad := sigma * 3
	x0, x1 := int(math.Floor(cx-rad)), int(math.Ceil(cx+rad))
	y0, y1 := int(math.Floor(cy-rad)), int(math.Ceil(cy+rad))
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, Canvas-1), min(y1, Canvas-1)
	if x0 > x1 || y0 > y1 {
		return
	}
	inv := 1 / (2 * sigma * sigma)
	invCore := 1 / (2 * (0.45 * sigma) * (0.45 * sigma))
	core := 0.9 * b
	cr, cg, cb := float32(c[0]*b), float32(c[1]*b), float32(c[2]*b)
	for y := y0; y <= y1; y++ {
		dy := float64(y) + 0.5 - cy
		row := y * Canvas
		for x := x0; x <= x1; x++ {
			dx := float64(x) + 0.5 - cx
			d2 := dx*dx + dy*dy
			g := float32(math.Exp(-d2 * inv))
			w := float32(core * math.Exp(-d2*invCore))
			i := (row + x) * 3
			sc.light[i] += cr*g + w
			sc.light[i+1] += cg*g + w
			sc.light[i+2] += cb*g + w
		}
	}
}

// boxBlur runs one horizontal and one vertical box pass over src into dst,
// using tmp; outside the canvas counts as dark.
func boxBlur(dst, src, tmp []float32, r int) {
	norm := float32(1) / float32(2*r+1)
	// Horizontal: src -> tmp.
	for y := 0; y < Canvas; y++ {
		row := y * Canvas * 3
		var s0, s1, s2 float32
		for x := 0; x <= r && x < Canvas; x++ {
			i := row + x*3
			s0, s1, s2 = s0+src[i], s1+src[i+1], s2+src[i+2]
		}
		for x := 0; x < Canvas; x++ {
			i := row + x*3
			tmp[i], tmp[i+1], tmp[i+2] = s0*norm, s1*norm, s2*norm
			if a := x + r + 1; a < Canvas {
				j := row + a*3
				s0, s1, s2 = s0+src[j], s1+src[j+1], s2+src[j+2]
			}
			if a := x - r; a >= 0 {
				j := row + a*3
				s0, s1, s2 = s0-src[j], s1-src[j+1], s2-src[j+2]
			}
		}
	}
	// Vertical: tmp -> dst.
	const stride = Canvas * 3
	for x := 0; x < Canvas; x++ {
		var s0, s1, s2 float32
		for y := 0; y <= r && y < Canvas; y++ {
			i := y*stride + x*3
			s0, s1, s2 = s0+tmp[i], s1+tmp[i+1], s2+tmp[i+2]
		}
		for y := 0; y < Canvas; y++ {
			i := y*stride + x*3
			dst[i], dst[i+1], dst[i+2] = s0*norm, s1*norm, s2*norm
			if a := y + r + 1; a < Canvas {
				j := a*stride + x*3
				s0, s1, s2 = s0+tmp[j], s1+tmp[j+1], s2+tmp[j+2]
			}
			if a := y - r; a >= 0 {
				j := a*stride + x*3
				s0, s1, s2 = s0-tmp[j], s1-tmp[j+1], s2-tmp[j+2]
			}
		}
	}
}

// render draws the scene into a premultiplied BGRA canvas, top row first, as
// UpdateLayeredWindow wants it. The buffer is reused between frames.
func (sc *scene) render() []byte {
	if sc.light == nil {
		sc.alloc()
	}
	if !sc.ready {
		sc.setup()
	}
	clear(sc.light)
	gain := sc.gain * sc.presence
	if gain > 0.001 {
		for i := range sc.dots {
			d := &sc.dots[i]
			b := d.b * gain
			if b < 0.01 {
				continue
			}
			sc.splat(d.x+centre, d.y+centre, d.sz, b, sc.col)
		}
		pc := mix(sc.col, rgb{1, 1, 1}, 0.5)
		for j := range sc.pearls {
			p := &sc.pearls[j]
			b := p.b * gain
			if b < 0.01 {
				continue
			}
			sc.splat(p.x+centre, p.y+centre, pearlSigma, b, pc)
		}
	}
	boxBlur(sc.glow, sc.light, sc.tmp, glowRadius)
	boxBlur(sc.tmp, sc.glow, sc.glow, glowRadius)
	boxBlur(sc.glow, sc.tmp, sc.tmp, glowRadius)

	halo := float32(sc.halo * sc.presence)
	hr, hg, hb := float32(haloCol[0]), float32(haloCol[1]), float32(haloCol[2])
	out := sc.out
	for i := 0; i < Canvas*Canvas; i++ {
		ah := sc.haloMask[i] * halo
		j := i * 3
		r := tone(sc.light[j] + glowGain*sc.glow[j])
		g := tone(sc.light[j+1] + glowGain*sc.glow[j+1])
		b := tone(sc.light[j+2] + glowGain*sc.glow[j+2])
		la := max(r, g, b) * alphaBoost
		if la > 1 {
			la = 1
		}
		if la < 0.002 && ah < 0.002 {
			out[i*4], out[i*4+1], out[i*4+2], out[i*4+3] = 0, 0, 0, 0
			continue
		}
		under := ah * (1 - la) // the halo showing through the light
		a := la + under
		out[i*4] = byte((b+hb*under)*255 + 0.5)
		out[i*4+1] = byte((g+hg*under)*255 + 0.5)
		out[i*4+2] = byte((r+hr*under)*255 + 0.5)
		out[i*4+3] = byte(a*255 + 0.5)
	}
	return out
}

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func smooth(e0, e1, v float64) float64 {
	t := clamp((v - e0) / (e1 - e0))
	return t * t * (3 - 2*t)
}
