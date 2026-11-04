package galaxy

import "math"

// The galaxy is drawn as light: particles and stars are splatted into a
// floating point buffer, which gets a two level bloom, an analytic core and
// shock ring, a tone map that turns bright light white, and finally a soft
// dark halo underneath so it reads on light backgrounds too.

const (
	expMax  = 12.0
	expSize = 4096
	bloom1  = 0.6 // tight glow around every particle
	bloom2  = 0.8 // the wide nebula glow
)

var (
	expTab  [expSize + 1]float64 // exp(-q) for q in [0, expMax]
	distTab [Canvas * Canvas]float64
	blurK   = gaussKernel(1.7, 4)
	haloTab [64 * haloRes]float64 // the halo's opacity by distance from the centre
)

const haloRes = 16

func init() {
	for i := range haloTab {
		haloTab[i] = math.Pow(1-smooth(30, 63, float64(i)/haloRes), 1.3)
	}
	for i := range expTab {
		expTab[i] = math.Exp(-float64(i) * expMax / expSize)
	}
	for y := 0; y < Canvas; y++ {
		for x := 0; x < Canvas; x++ {
			distTab[y*Canvas+x] = math.Hypot(float64(x)+0.5-center, float64(y)+0.5-center)
		}
	}
}

// fexp is exp(-q) for q >= 0, from the table.
func fexp(q float64) float64 {
	if q >= expMax {
		return 0
	}
	return expTab[int(q*(expSize/expMax)+0.5)]
}

func gaussKernel(sigma float64, rad int) []float64 {
	k := make([]float64, 2*rad+1)
	sum := 0.0
	for i := range k {
		d := float64(i - rad)
		k[i] = math.Exp(-d * d / (2 * sigma * sigma))
		sum += k[i]
	}
	for i := range k {
		k[i] /= sum
	}
	return k
}

// splat adds a round gaussian dot of light b with colour c at x, y (pixels).
func (sc *scene) splat(x, y, sig, b float64, c rgb) {
	inv := 1 / (2 * sig * sig)
	rad := int(math.Ceil(sig * 2.6))
	cx, cy := int(math.Floor(x)), int(math.Floor(y))
	x0, x1 := max(cx-rad, 0), min(cx+rad, Canvas-1)
	y0, y1 := max(cy-rad, 0), min(cy+rad, Canvas-1)
	cr, cg, cb := b*c[0], b*c[1], b*c[2]
	for j := y0; j <= y1; j++ {
		dy := float64(j) + 0.5 - y
		dy2 := dy * dy * inv
		row := j * Canvas
		for i := x0; i <= x1; i++ {
			dx := float64(i) + 0.5 - x
			q := dx*dx*inv + dy2
			if q >= expMax {
				continue
			}
			w := expTab[int(q*(expSize/expMax)+0.5)]
			o := (row + i) * 3
			sc.acc[o] += w * cr
			sc.acc[o+1] += w * cg
			sc.acc[o+2] += w * cb
		}
	}
}

// line adds a thin streak of light from x0, y0 to x1, y1.
func (sc *scene) line(x0, y0, x1, y1, sig, b float64, c rgb) {
	d := math.Hypot(x1-x0, y1-y0)
	n := 1
	if d > 0.7 {
		n = min(int(d/0.7)+1, 10)
	}
	if n == 1 {
		sc.splat(x1, y1, sig, b, c)
		return
	}
	// A streak is a touch dimmer per dot but brighter at its head.
	bb := b / math.Sqrt(float64(n))
	for i := 0; i < n; i++ {
		f := float64(i) / float64(n-1)
		sc.splat(mix(x0, x1, f), mix(y0, y1, f), sig, bb*(0.45+0.55*f), c)
	}
}

func (sc *scene) drawStars(pv float64) {
	starC, glowC := rgb{0.80, 0.90, 1.00}, rgb{0.62, 0.52, 1.00}
	if sc.kind == Done {
		starC, glowC = mixc(starC, mint, 0.4), mixc(glowC, mint, 0.6)
	}
	for _, st := range sc.stars {
		if st.a < 0.005 {
			continue
		}
		x := center + sc.shake + st.r*math.Cos(st.ang)
		y := center + st.r*math.Sin(st.ang)
		tw := 1 + 0.12*math.Sin(sc.t*3.1+st.ang*3)
		a := st.a * tw
		sc.splat(x, y, 3.0, 0.75*a, glowC)
		sc.splat(x, y, 1.3, 2.8*a, starC)
		sc.splat(x, y, 0.65, 3*a, white)
		// Four short diffraction spikes make each star a countable point.
		for d := 1; d <= 9; d++ {
			w := 1.6 * a * math.Exp(-float64(d)/2.8)
			fd := float64(d)
			sc.splat(x+fd, y, 0.45, w, starC)
			sc.splat(x-fd, y, 0.45, w, starC)
			sc.splat(x, y+fd, 0.45, w, starC)
			sc.splat(x, y-fd, 0.45, w, starC)
		}
	}
}

// down halves src (w x w) into dst, averaging 2x2 blocks.
func down(dst, src []float64, w int) {
	h := w / 2
	for y := 0; y < h; y++ {
		for x := 0; x < h; x++ {
			a := ((2*y)*w + 2*x) * 3
			b := a + w*3
			o := (y*h + x) * 3
			for j := 0; j < 3; j++ {
				dst[o+j] = 0.25 * (src[a+j] + src[a+3+j] + src[b+j] + src[b+3+j])
			}
		}
	}
}

// blur is a separable gaussian over buf (w x w), using tmp.
func blur(buf, tmp []float64, w int) {
	rad := len(blurK) / 2
	for y := 0; y < w; y++ {
		for x := 0; x < w; x++ {
			var s0, s1, s2 float64
			for i, kv := range blurK {
				xx := x + i - rad
				if xx < 0 || xx >= w {
					continue
				}
				o := (y*w + xx) * 3
				s0 += kv * buf[o]
				s1 += kv * buf[o+1]
				s2 += kv * buf[o+2]
			}
			o := (y*w + x) * 3
			tmp[o], tmp[o+1], tmp[o+2] = s0, s1, s2
		}
	}
	for y := 0; y < w; y++ {
		for x := 0; x < w; x++ {
			var s0, s1, s2 float64
			for i, kv := range blurK {
				yy := y + i - rad
				if yy < 0 || yy >= w {
					continue
				}
				o := (yy*w + x) * 3
				s0 += kv * tmp[o]
				s1 += kv * tmp[o+1]
				s2 += kv * tmp[o+2]
			}
			o := (y*w + x) * 3
			buf[o], buf[o+1], buf[o+2] = s0, s1, s2
		}
	}
}

// sample reads buf (w x w) bilinearly at x, y in its own pixels.
func sample(buf []float64, w int, x, y float64) (float64, float64, float64) {
	x, y = min(max(x, 0), float64(w-1)), min(max(y, 0), float64(w-1))
	ix, iy := int(x), int(y)
	fx, fy := x-float64(ix), y-float64(iy)
	ix1, iy1 := min(ix+1, w-1), min(iy+1, w-1)
	a, b := (iy*w+ix)*3, (iy*w+ix1)*3
	c, d := (iy1*w+ix)*3, (iy1*w+ix1)*3
	w00, w10, w01, w11 := (1-fx)*(1-fy), fx*(1-fy), (1-fx)*fy, fx*fy
	return w00*buf[a] + w10*buf[b] + w01*buf[c] + w11*buf[d],
		w00*buf[a+1] + w10*buf[b+1] + w01*buf[c+1] + w11*buf[d+1],
		w00*buf[a+2] + w10*buf[b+2] + w01*buf[c+2] + w11*buf[d+2]
}

// render draws the galaxy into a premultiplied BGRA canvas, top row first, as
// UpdateLayeredWindow wants it.
func (sc *scene) render() []byte {
	px := make([]byte, Canvas*Canvas*4)
	if !sc.ready {
		return px
	}
	clear(sc.acc)
	pv := smooth(0, 1, sc.present)
	for i := range sc.ps {
		p := &sc.ps[i]
		if p.b < 0.004 {
			continue
		}
		sc.line(p.x0, p.y0, p.x1, p.y1, p.sig, p.b, p.c)
	}
	sc.drawStars(pv)

	const h1, h2 = Canvas / 2, Canvas / 4
	down(sc.b1, sc.acc, Canvas)
	down(sc.b2, sc.b1, h1)
	blur(sc.b1, sc.tmp, h1)
	blur(sc.b2, sc.tmp, h2)

	c := &sc.cur
	// The core: a hot centre in a wide soft glow, brighter with a flare.
	flare := sc.burstLight()
	coreB := c.core * pv
	coreC := c.coreC
	if flare > 0 {
		coreB += flare * (3 + 2*math.Min(sc.merged, 3))
		coreC = mixc(coreC, rgb{0.85, 1, 0.92}, flare)
	}
	cs := c.coreW * (0.45 + 0.55*pv)
	inv1 := 1 / (2 * sq(2.0*cs))
	inv2 := 1 / (2 * sq(6.5*cs))
	// The shock ring of the Done flare.
	ringR, ringB := 0.0, 0.0
	if sc.burst >= 0 {
		a := (sc.t - sc.burst) / 1.0
		if a < 1 {
			ringR = 4 + 48*(1-math.Pow(1-a, 2.2))
			ringB = 1.2 * math.Pow(1-a, 1.6)
		}
	}
	ringInv := 1 / (2 * sq(1.4+3*(ringR/62)))
	// The pulsing amber ring of a problem.
	warnR := 46 * (0.3 + 0.7*pv)
	warnB := 0.85 * c.warn * pv
	const warnInv = 1 / (2 * 0.95 * 0.95)
	haloScale := 1 / (0.35 + 0.65*pv)
	haloA := 0.86 * pv

	for y := 0; y < Canvas; y++ {
		sy1 := (float64(y)+0.5)/2 - 0.5
		sy2 := (float64(y)+0.5)/4 - 0.5
		for x := 0; x < Canvas; x++ {
			i := y*Canvas + x
			d := distTab[i]
			if d > 63.5 {
				continue
			}
			o := i * 3
			r, g, b := sc.acc[o], sc.acc[o+1], sc.acc[o+2]
			r1, g1, b1 := sample(sc.b1, h1, (float64(x)+0.5)/2-0.5, sy1)
			r2, g2, b2 := sample(sc.b2, h2, (float64(x)+0.5)/4-0.5, sy2)
			r += bloom1*r1 + bloom2*r2
			g += bloom1*g1 + bloom2*g2
			b += bloom1*b1 + bloom2*b2
			if coreB > 0.001 {
				dd := d * d
				w := coreB * (1.6*fexp(dd*inv1) + 0.35*fexp(dd*inv2))
				r += w * coreC[0]
				g += w * coreC[1]
				b += w * coreC[2]
			}
			if ringB > 0.001 {
				w := ringB * fexp(sq(d-ringR)*ringInv)
				r += w * aqua[0] * 0.9
				g += w * aqua[1]
				b += w * aqua[2] * 0.85
			}
			if warnB > 0.002 {
				dr := d - warnR
				w := warnB * (fexp(dr*dr*warnInv) + 0.18*fexp(dr*dr*warnInv/9))
				r += w * amber[0]
				g += w * amber[1]
				b += w * amber[2]
			}
			if d > 52 { // nothing reaches the window's edge
				f := 1 - smooth(52, 63.5, d)
				r, g, b = r*f, g*f, b*f
			}
			// Tone map: strong light turns white, faint light keeps its colour.
			r, g, b = 1-fexp(r), 1-fexp(g), 1-fexp(b)
			la := max(r, g, b)
			// The halo underneath.
			h := 0.0
			if hd := d * haloScale; hd < 63 {
				h = haloA * haloTab[int(hd*haloRes)]
			}
			if la <= 0 && h <= 0 {
				continue
			}
			k := h * (1 - la)
			r += halo[0] * k
			g += halo[1] * k
			b += halo[2] * k
			a := la + k
			q := i * 4
			px[q] = byte(255*clamp(b) + 0.5)
			px[q+1] = byte(255*clamp(g) + 0.5)
			px[q+2] = byte(255*clamp(r) + 0.5)
			px[q+3] = byte(255*clamp(a) + 0.5)
		}
	}
	return px
}

func sq(v float64) float64 { return v * v }
