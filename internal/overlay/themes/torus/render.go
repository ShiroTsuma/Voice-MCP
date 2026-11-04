package torus

import "math"

const (
	half   = Canvas / 2
	center = Canvas / 2.0
	camera = 112.0 // the eye's distance from the ring's centre, in pixels
)

var (
	// expTab[i] is exp(-i/expRes): a gaussian falloff by squared distance.
	expTab [expRes*expMax + 1]float32
	// toneTab maps light to 0..1 as 1-exp(-x), softly, so bright cores roll
	// off into white instead of clipping.
	toneTab [toneRes*toneMax + 2]float64
	// haloTab is the dark halo's opacity by pixel: it keeps the light readable
	// on a white page as well as a dark one.
	haloTab [Canvas * Canvas]float64
	// bayer is an ordered dither, a fixed pattern, so it never flickers.
	bayer = [16]float64{0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5}
	// upsample's taps: half-resolution cell and weight per full-resolution pixel.
	upIdx [Canvas]int
	upW   [Canvas]float64
)

const (
	expRes  = 64
	expMax  = 9
	toneRes = 256
	toneMax = 10
)

func init() {
	for i := range expTab {
		expTab[i] = float32(math.Exp(-float64(i) / expRes))
	}
	for i := range toneTab {
		toneTab[i] = 1 - math.Exp(-float64(i)/toneRes)
	}
	for y := 0; y < Canvas; y++ {
		for x := 0; x < Canvas; x++ {
			r := math.Hypot(float64(x)+0.5-center, float64(y)+0.5-center)
			h := smooth(center-1, 30, r)
			haloTab[y*Canvas+x] = 0.88 * h * (1.5 - 0.5*h)
		}
	}
	for x := 0; x < Canvas; x++ {
		f := (float64(x)+0.5)/2 - 0.5
		i := int(math.Floor(f))
		w := f - float64(i)
		if i < 0 {
			i, w = 0, 0
		}
		if i >= half-1 {
			i, w = half-2, 1
		}
		upIdx[x], upW[x] = i, w
	}
}

func tone(x float64) float64 {
	if x <= 0 {
		return 0
	}
	f := x * toneRes
	if f >= toneRes*toneMax {
		return toneTab[toneRes*toneMax]
	}
	i := int(f)
	w := f - float64(i)
	return toneTab[i] + (toneTab[i+1]-toneTab[i])*w
}

// render projects the particles in perspective (near ones larger and
// brighter), splats each as a small gaussian, adds a two-scale bloom in the
// state's colour and lays it all over the dark halo, into a premultiplied BGRA
// canvas, top row first, as UpdateLayeredWindow wants it.
func (sc *scene) render() []byte {
	px := make([]byte, Canvas*Canvas*4)
	if !sc.ready || sc.presence <= 0 {
		return px
	}
	vis := smooth(0, 1, sc.presence)
	clear(sc.light[:])
	clear(sc.glow[:])

	t := sc.t
	cphi, sphi := math.Cos(sc.phi), math.Sin(sc.phi)
	ct, st := math.Cos(sc.tilt), math.Sin(sc.tilt)
	cy, sy := math.Cos(sc.yaw), math.Sin(sc.yaw)
	cr, sr := math.Cos(sc.roll), math.Sin(sc.roll)
	col := sc.col
	// Cores are nearly white with a breath of the state's colour: icy.
	var core rgb
	for j := range core {
		core[j] = 0.42*col[j] + 0.58
	}
	gain := sc.gain * vis * (1 + 0.9*sc.flash)
	wave, turb, comet := sc.wave, sc.turb, sc.comet

	for i := range sc.ps {
		p := &sc.ps[i]
		b := p.b * gain
		if b < 0.004 {
			continue
		}
		x, y, z := p.pos[0], p.pos[1], p.pos[2]
		rad := math.Hypot(x, y)
		ux, uy := 1.0, 0.0
		if rad > 1e-6 {
			ux, uy = x/rad, y/rad
		}
		var dr, dz float64
		if wave > 0.002 || comet > 0.002 {
			a := math.Atan2(uy, ux)
			if wave > 0.002 {
				// The voice runs round the ring as ripples, in and out and
				// up and down.
				dr += wave * (3.0*math.Sin(3*a-3.3*t) + 1.9*math.Sin(5*a+4.7*t+1) + 1.1*math.Sin(2*a+2.1*t))
				dz += wave * 2.6 * math.Sin(4*a-2.6*t+0.5)
				b *= 1 + 0.35*wave*math.Sin(3*a-3.3*t)
			}
			if comet > 0.002 {
				d := math.Mod(sc.head-a, 2*math.Pi)
				if d < 0 {
					d += 2 * math.Pi
				}
				m := 0.1 + 1.5*math.Exp(-d*0.8) + 1.3*math.Exp(-d*6)
				b *= 1 + (m-1)*comet
				dr += comet * 2.2 * math.Exp(-d*4)
			}
		}
		x += ux*dr + turb*math.Sin(t*p.w[0]+p.p[0])
		y += uy*dr + turb*math.Sin(t*p.w[1]+p.p[1])
		z += dz + turb*math.Sin(t*p.w[2]+p.p[2])

		// Spin round the ring's axis, tilt it back (the bottom comes toward
		// us), shake, turn it on screen, then perspective.
		x1, y1 := x*cphi-y*sphi, x*sphi+y*cphi
		y2, z2 := y1*ct+z*st, -y1*st+z*ct
		x3, z3 := x1*cy+z2*sy, -x1*sy+z2*cy
		x4, y4 := x3*cr-y2*sr, x3*sr+y2*cr
		s := camera / (camera + z3)
		fx, fy := center+x4*s, center+y4*s

		depth := s * s * math.Sqrt(s) // near: bigger and much brighter
		b *= depth * (0.8 + 0.2*math.Sin(t*p.tw+p.ph))
		sigma := math.Max(0.62, p.size*s)
		sc.splat(fx, fy, sigma, b*1.5, core)

		// Into the glow, at half resolution.
		gx, gy := fx/2-0.5, fy/2-0.5
		ix, iy := int(math.Floor(gx)), int(math.Floor(gy))
		wx, wy := gx-float64(ix), gy-float64(iy)
		if ix >= 0 && iy >= 0 && ix < half-1 && iy < half-1 {
			g := &sc.glow
			for _, c := range [4]struct {
				o int
				w float64
			}{{0, (1 - wx) * (1 - wy)}, {1, wx * (1 - wy)}, {half, (1 - wx) * wy}, {half + 1, wx * wy}} {
				cell := &g[iy*half+ix+c.o]
				for j := range cell {
					cell[j] += b * c.w * col[j]
				}
			}
		}
	}

	// Bloom: a tight glow that makes the points shine, and a wide haze.
	sc.small = sc.glow
	blur(&sc.small, &sc.tmp, 1)
	sc.large = sc.small
	blur(&sc.large, &sc.tmp, 3)

	dark := rgb{0.010 + 0.035*col[0], 0.018 + 0.035*col[1], 0.026 + 0.035*col[2]}
	for y := 0; y < Canvas; y++ {
		y0, wy := upIdx[y], upW[y]
		for x := 0; x < Canvas; x++ {
			x0, wx := upIdx[x], upW[x]
			i := y*Canvas + x
			o := y0*half + x0
			w00, w10, w01, w11 := (1-wx)*(1-wy), wx*(1-wy), (1-wx)*wy, wx*wy
			var l rgb
			for j := range l {
				sm := sc.small[o][j]*w00 + sc.small[o+1][j]*w10 + sc.small[o+half][j]*w01 + sc.small[o+half+1][j]*w11
				lg := sc.large[o][j]*w00 + sc.large[o+1][j]*w10 + sc.large[o+half][j]*w01 + sc.large[o+half+1][j]*w11
				l[j] = tone(sc.light[i][j] + 0.55*sm + 1.25*lg)
			}
			al := math.Max(l[0], math.Max(l[1], l[2]))
			h := haloTab[i] * vis
			a := al + h*(1-al)
			if a <= 0 {
				continue
			}
			d := (bayer[(y&3)*4+(x&3)] + 0.5) / 16
			ab := byte(math.Min(255, a*255+d))
			px[i*4+3] = ab
			for j, off := range [3]int{2, 1, 0} { // BGRA
				v := l[j] + dark[j]*h*(1-al)
				px[i*4+off] = min(ab, byte(math.Min(255, v*255+d)))
			}
		}
	}
	return px
}

// splat adds a gaussian point of light of peak b at (fx, fy).
func (sc *scene) splat(fx, fy, sigma, b float64, c rgb) {
	r := int(math.Ceil(2.9 * sigma))
	inv := expRes / (2 * sigma * sigma)
	x0, x1 := max(0, int(fx)-r), min(Canvas-1, int(fx)+r)
	y0, y1 := max(0, int(fy)-r), min(Canvas-1, int(fy)+r)
	cr, cg, cb := b*c[0], b*c[1], b*c[2]
	for y := y0; y <= y1; y++ {
		dy := float64(y) + 0.5 - fy
		dy2 := dy * dy
		row := sc.light[y*Canvas:]
		for x := x0; x <= x1; x++ {
			dx := float64(x) + 0.5 - fx
			k := int((dx*dx + dy2) * inv)
			if k >= len(expTab) {
				continue
			}
			v := float64(expTab[k])
			row[x][0] += cr * v
			row[x][1] += cg * v
			row[x][2] += cb * v
		}
	}
}

// blur runs three box blurs of radius r each way: close to a gaussian.
func blur(buf, tmp *[half * half]rgb, r int) {
	for range 3 {
		boxPass(buf, tmp, r, 1, half) // rows
		boxPass(tmp, buf, r, half, 1) // columns
	}
}

// boxPass averages src along lines (step apart, lines `next` apart) into dst.
func boxPass(src, dst *[half * half]rgb, r, step, next int) {
	n := float64(2*r + 1)
	for line := 0; line < half; line++ {
		base := line * next
		var sum rgb
		for k := -r; k <= r; k++ {
			if k >= 0 && k < half {
				for j := range sum {
					sum[j] += src[base+k*step][j]
				}
			}
		}
		for k := 0; k < half; k++ {
			for j := range sum {
				dst[base+k*step][j] = sum[j] / n
			}
			if out := k - r; out >= 0 {
				for j := range sum {
					sum[j] -= src[base+out*step][j]
				}
			}
			if in := k + r + 1; in < half {
				for j := range sum {
					sum[j] += src[base+in*step][j]
				}
			}
		}
	}
}

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// smooth is smoothstep; with e0 > e1 it falls instead of rising.
func smooth(e0, e1, v float64) float64 {
	t := clamp((v - e0) / (e1 - e0))
	return t * t * (3 - 2*t)
}
