package reference

import "math"

// expTab is exp(-x) for x = i/64, i in 0..64*9: the shape of every particle.
var expTab = func() [64*9 + 1]float32 {
	var t [64*9 + 1]float32
	for i := range t {
		t[i] = float32(math.Exp(-float64(i) / 64))
	}
	return t
}()

// splat adds a gaussian dot of the given sigma (pixels) and peak amplitude
// at (x, y) to buf.
func splat(buf []float32, x, y, sigma, amp float64) {
	const c = Canvas
	r := int(math.Ceil(3 * sigma))
	ix, iy := int(math.Floor(x)), int(math.Floor(y))
	inv := 1 / (2 * sigma * sigma)
	a := float32(amp)
	for yy := max(iy-r, 0); yy <= min(iy+r+1, c-1); yy++ {
		dy := float64(yy) + 0.5 - y
		dy2 := dy * dy
		row := buf[yy*c : yy*c+c]
		for xx := max(ix-r, 0); xx <= min(ix+r+1, c-1); xx++ {
			dx := float64(xx) + 0.5 - x
			e := (dx*dx + dy2) * inv
			if e >= 9 {
				continue
			}
			row[xx] += a * expTab[int(e*64)]
		}
	}
}

// kernel is a normalised 1-D gaussian for a separable blur.
type kernel struct {
	r int
	w []float32
}

func newKernel(sigma float64) kernel {
	r := int(math.Ceil(2.6 * sigma))
	k := kernel{r: r, w: make([]float32, 2*r+1)}
	sum := 0.0
	for i := range k.w {
		d := float64(i - r)
		k.w[i] = float32(math.Exp(-d * d / (2 * sigma * sigma)))
		sum += float64(k.w[i])
	}
	for i := range k.w {
		k.w[i] /= float32(sum)
	}
	return k
}

var (
	glowK = newKernel(2.2) // the shine round each particle
	mistK = newKernel(4.5) // the dark mist behind the particles
)

// blur writes the blur of src into dst, using tmp, within rows y0..y1 and
// columns x0..x1 (inclusive, already padded by the kernel's radius).
func (k kernel) blur(dst, tmp, src []float32, x0, y0, x1, y1 int) {
	const c = Canvas
	r := k.r
	for y := y0; y <= y1; y++ {
		row := src[y*c : y*c+c]
		out := tmp[y*c : y*c+c]
		for x := x0; x <= x1; x++ {
			var s float32
			for i := max(x-r, x0); i <= min(x+r, x1); i++ {
				s += row[i] * k.w[i-x+r]
			}
			out[x] = s
		}
	}
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			var s float32
			for i := max(y-r, y0); i <= min(y+r, y1); i++ {
				s += tmp[i*c+x] * k.w[i-y+r]
			}
			dst[y*c+x] = s
		}
	}
}

// bayer is an 8x8 ordered-dither threshold matrix, 0..1: a fixed pattern
// that breaks the 8-bit steps of the dark gradients into something the eye
// reads as smooth, without changing from frame to frame.
var bayer = func() [64]float64 {
	var m [64]float64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			v := 0
			for bit, xb, yb := 0, x, y; bit < 3; bit, xb, yb = bit+1, xb>>1, yb>>1 {
				v = v<<2 | (xb & 1) ^ (yb&1)<<1 | yb&1
			}
			m[y*8+x] = (float64(v) + 0.5) / 64
		}
	}
	return m
}()

// render draws the halo and the particles into a premultiplied BGRA canvas,
// top row first, as UpdateLayeredWindow wants it.
func (sc *scene) render() []byte {
	const c = Canvas
	if !sc.ready {
		sc.init()
	}
	px := make([]byte, c*c*4)
	core := sc.core
	for i := range core {
		core[i] = 0
	}
	cx := c/2 + sc.shake
	x0, y0, x1, y1 := c, c, -1, -1
	mark := func(x, y, r float64) {
		x0 = min(x0, int(x-r)-1)
		y0 = min(y0, int(y-r)-1)
		x1 = max(x1, int(x+r)+2)
		y1 = max(y1, int(y+r)+2)
	}
	for i := range sc.p {
		p := &sc.p[i]
		if p.bri < 0.004 {
			continue
		}
		s, co := math.Sincos(p.ang)
		x, y := cx+p.rad*co, c/2+p.rad*s
		if x < -4 || y < -4 || x > c+4 || y > c+4 {
			continue
		}
		sigma := 0.5 + 0.4*p.size
		splat(core, x, y, sigma, p.bri*p.size)
		mark(x, y, 3*sigma)
	}
	if sc.flare > 0.01 {
		splat(core, cx, c/2, 7, 1.4*sc.flare)
		mark(cx, c/2, 21)
	}
	light := x1 >= 0
	if light {
		pad := glowK.r + mistK.r
		x0, y0 = max(x0-pad, 0), max(y0-pad, 0)
		x1, y1 = min(x1+pad, c-1), min(y1+pad, c-1)
		glowK.blur(sc.blur, sc.tmp, core, x0, y0, x1, y1)
		mistK.blur(sc.wide, sc.tmp, sc.blur, x0, y0, x1, y1)
	}
	tint, hot, halo := sc.tint, sc.hot, sc.halo
	presence := float32(sc.presence)
	for y := 0; y < c; y++ {
		for x := 0; x < c; x++ {
			i := y*c + x
			ha := presence * sc.haloMask[i]
			var l float32
			if light && x >= x0 && x <= x1 && y >= y0 && y <= y1 {
				l = (core[i] + 0.5*sc.blur[i]) * sc.edge[i]
				// The mist: dark teal gathering under the particles, so they
				// read on a light desktop too.
				mist := 0.72 * (1 - float32(math.Exp(-6*float64(sc.wide[i])))) * sc.edge[i]
				ha += mist * (1 - ha)
			}
			if l < 0.002 && ha < 0.002 {
				continue
			}
			L := float64(l)
			a := 1 - math.Exp(-2*L)
			w := smooth(0.3, 2.0, L)
			A := a + float64(ha)*(1-a)
			d := bayer[(y&7)*8+(x&7)]
			o := i * 4
			alpha := byte(math.Floor(255*clamp(A) + d))
			for j, off := range [3]int{2, 1, 0} { // BGRA
				col := tint[j]*(1-w) + hot[j]*w
				v := col*a + halo[j]*float64(ha)*(1-a)
				px[o+off] = min(byte(math.Floor(255*clamp(v)+d)), alpha)
			}
			px[o+3] = alpha
		}
	}
	return px
}
