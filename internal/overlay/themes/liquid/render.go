package liquid

import "math"

type ball struct{ x, y, r float64 }

// balls are the metaballs of the liquid: the core's lumps, the waiting
// droplets and the drops a spark pulls out of the core or into it.
func (sc *scene) balls(buf []ball) []ball {
	core := sc.coreSize()
	if core > 0.3 {
		buf = append(buf, ball{sc.shake, 0, core * 0.8})
		for k := 0; k < 3; k++ {
			fk := float64(k)
			a := sc.churn*(1+0.17*fk) + fk*2*math.Pi/3
			d := core * sc.lk.orbit * (1 + 0.3*math.Sin(sc.t*1.9+fk*2.1))
			buf = append(buf, ball{sc.shake + d*math.Cos(a), d * math.Sin(a), core * (0.5 + 0.07*math.Sin(sc.t*2.7+fk))})
		}
	}
	for _, d := range sc.q {
		if d.rad > 0.2 {
			buf = append(buf, ball{d.dist * math.Cos(d.ang), d.dist * math.Sin(d.ang), d.rad})
		}
	}
	if core > 1 {
		drops := 0
		for i := range sc.sp {
			p := &sc.sp[i]
			if p.vis <= 0 || p.burst || p.r > core+11 || p.r < core*0.5 || drops == 6 {
				continue
			}
			r := 3.1 * (1 - smooth(core-1, core+11, p.r)) * math.Min(1, core/6)
			if r > 0.4 {
				buf = append(buf, ball{p.r*math.Cos(p.th) + sc.shake, p.r*math.Sin(p.th) + p.dy, r})
				drops++
			}
		}
	}
	return buf
}

// Lookup tables: exp(-x) and each pixel's distance from the centre.
const expStep = 128

var (
	expTab  [24*expStep + 2]float32
	distTab [Canvas * Canvas]float32
)

func init() {
	for i := range expTab {
		expTab[i] = float32(math.Exp(-float64(i) / expStep))
	}
	for y := 0; y < Canvas; y++ {
		for x := 0; x < Canvas; x++ {
			fx, fy := float64(x)+0.5-Canvas/2.0, float64(y)+0.5-Canvas/2.0
			distTab[y*Canvas+x] = float32(math.Hypot(fx, fy))
		}
	}
}

// fexp is exp(-x) for x >= 0, interpolated from the table.
func fexp(x float32) float32 {
	if x >= 24 {
		return 0
	}
	f := x * expStep
	i := int(f)
	w := f - float32(i)
	return expTab[i] + (expTab[i+1]-expTab[i])*w
}

// segDist is the distance from p to the segment a-b.
func segDist(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	t := clamp(((px-ax)*dx + (py-ay)*dy) / (dx*dx + dy*dy))
	ex, ey := px-ax-t*dx, py-ay-t*dy
	return math.Sqrt(ex*ex + ey*ey)
}

// mark is how much of the core's glyph covers (x, y), relative to the core's
// centre: a check drawn stroke by stroke after a delivery, a "!" on a problem.
func (sc *scene) mark(x, y, s float64) float64 {
	x, y = x/s, y/s
	hw := 1.45
	var d float64
	if sc.markKind == Done {
		// Two strokes, the short one first; p is how far along the pen is.
		ax, ay, bx, by, cx, cy := -4.8, 0.2, -1.6, 3.6, 5.0, -3.9
		l1 := math.Hypot(bx-ax, by-ay)
		l2 := math.Hypot(cx-bx, cy-by)
		p := smooth(0, 1, sc.markP) * (l1 + l2)
		if p <= 0.01 {
			return 0
		}
		if p < l1 {
			f := p / l1
			d = segDist(x, y, ax, ay, ax+(bx-ax)*f, ay+(by-ay)*f)
		} else {
			f := (p - l1) / l2
			d = math.Min(segDist(x, y, ax, ay, bx, by), segDist(x, y, bx, by, bx+(cx-bx)*f, by+(cy-by)*f))
		}
	} else {
		g := smooth(0, 1, sc.markP)
		d = math.Min(segDist(x, y, 0, -5.4, 0, -5.4+6.6*g), math.Hypot(x, y-4.2)-0.35*g)
		hw = 1.35 * g
	}
	return clamp((hw-d)*s + 0.5)
}

// render draws the halo, the liquid and the sparks into a premultiplied BGRA
// canvas, top row first, as UpdateLayeredWindow wants it.
func (sc *scene) render() []byte {
	px := make([]byte, Canvas*Canvas*4)
	if !sc.ready {
		return px
	}
	const c = Canvas / 2.0
	em := &sc.em
	for i := range em {
		em[i] = 0
	}
	lk := &sc.lk
	tint := lk.tint
	t0, t1, t2 := float32(tint[0]), float32(tint[1]), float32(tint[2])
	pres := smooth(0, 1, sc.pres)
	core := sc.coreSize()

	// Sparks: a white-hot point in a coloured glow, added up as light.
	hide := smooth(0, 0.5, sc.pres)
	head := sc.t * 6.5
	for i := range sc.sp {
		p := &sc.sp[i]
		if p.vis <= 0 {
			continue
		}
		in := p.vis * lk.bright * hide * (0.66 + 0.34*math.Sin(sc.t*p.tw+p.ph))
		in *= smooth(core*0.6, core*1.25+1, p.r)
		in *= 0.8 + 0.2*math.Cos(p.th-sc.t*0.45)
		if lk.comet > 0.01 {
			d := wrap(head - p.th)
			cm := math.Exp(-math.Max(d, 0)/1.2) * math.Exp(math.Min(d, 0)*5)
			in *= 1 + lk.comet*(0.12+1.2*cm-1)
		}
		if p.burst {
			in *= 1 - smooth(32, 54, p.r)
		}
		in *= 1 - smooth(8, 34, p.dy)
		if in < 0.01 {
			continue
		}
		x := c + p.r*math.Cos(p.th) + sc.shake
		y := c + p.r*math.Sin(p.th) + p.dy
		s1 := 0.7 + 0.5*p.size
		s2 := 2.2 + 1.4*p.size
		i1 := float32(in * (1.25 + 0.8*p.size))
		i2 := float32(in * 0.42)
		k1, k2 := float32(1/(2*s1*s1)), float32(1/(2*s2*s2))
		x0, x1 := max(0, int(x-7)), min(Canvas-1, int(x+7))
		y0, y1 := max(0, int(y-7)), min(Canvas-1, int(y+7))
		for py := y0; py <= y1; py++ {
			dy := float32(float64(py) + 0.5 - y)
			for px := x0; px <= x1; px++ {
				dx := float32(float64(px) + 0.5 - x)
				d2 := dx*dx + dy*dy
				if d2 > 49 {
					continue
				}
				g1 := fexp(d2*k1) * i1
				g2 := fexp(d2*k2) * i2
				j := (py*Canvas + px) * 3
				em[j] += g1 + g2*t0
				em[j+1] += g1 + g2*t1
				em[j+2] += g1 + g2*t2
			}
		}
	}

	// The liquid's extent, glow included.
	var bb [24]ball
	balls := sc.balls(bb[:0])
	bx0, by0, bx1, by1 := Canvas, Canvas, -1, -1
	for i, b := range balls {
		e := b.r * 3.2
		if i == 0 {
			e = core * 3.6
		}
		bx0 = min(bx0, max(0, int(c+b.x-e)))
		by0 = min(by0, max(0, int(c+b.y-e)))
		bx1 = max(bx1, min(Canvas-1, int(c+b.x+e)+1))
		by1 = max(by1, min(Canvas-1, int(c+b.y+e)+1))
	}
	// Body colours: a saturated edge, a white-hot depth.
	edge := rgb{tint[0]*0.78 + 0.04, tint[1]*0.78 + 0.05, tint[2]*0.78 + 0.08}
	ink := rgb{tint[0] * 0.16, tint[1] * 0.16, tint[2] * 0.2}
	fl := sc.flash
	glowTint := rgb{tint[0]*(1-fl) + fl, tint[1]*(1-fl) + fl, tint[2]*(1-fl) + fl}
	glowAmp := 0.55 + 0.9*fl
	markOn := sc.markA > 0.01 && core > 3

	// The halo (a soft dark disc, so it reads on light desktops too) and the
	// light hanging around the ring, by distance from the centre.
	var haloTab, atmTab [92*4 + 2]float32 // the corners are 90.5 px out
	// Flat in the middle with a soft edge; it grows as everything appears.
	haloR := math.Min(48, (lk.ringR+13)*(0.45+0.55*pres))
	for i := range haloTab {
		d := float64(i) / 4
		q := d / haloR
		q *= q
		haloTab[i] = float32(0.78 * pres * math.Exp(-q*q) * (1 - smooth(50, 63, d)))
		dr := d - lk.ringR
		atmTab[i] = float32(0.075 * pres * math.Exp(-dr*dr/169) * (1 - smooth(48, 63, d)))
	}

	for y := 0; y < Canvas; y++ {
		fy := float64(y) + 0.5 - c
		rowBalls := y >= by0 && y <= by1
		for x := 0; x < Canvas; x++ {
			pi := y*Canvas + x
			dc := distTab[pi] * 4
			di := int(dc)
			w := dc - float32(di)
			ha := float64(haloTab[di] + (haloTab[di+1]-haloTab[di])*w)
			atm := atmTab[di] + (atmTab[di+1]-atmTab[di])*w
			r, g, b, a := 0.025*ha, 0.04*ha, 0.07*ha, ha
			j := pi * 3
			er := em[j] + atm*t0
			eg := em[j+1] + atm*t1
			eb := em[j+2] + atm*t2

			if rowBalls && x >= bx0 && x <= bx1 {
				fx := float64(x) + 0.5 - c
				var f, gx, gy float64
				for _, bl := range balls {
					dx, dy := fx-bl.x, fy-bl.y
					d2 := math.Max(dx*dx+dy*dy, 0.5)
					v := bl.r * bl.r / d2
					f += v
					gg := v / d2
					gx += gg * dx
					gy += gg * dy
				}
				if f > 0.12 {
					grad := 2*math.Hypot(gx, gy) + 1e-6
					ca := 1.0
					if f < 1.6 { // deeper in, the field is too steep to judge by
						ca = clamp(0.5 + (f-1)/grad)
					}
					if ca > 0 {
						depth := smooth(1, 3.6, f)
						// Outward normal against a light from the top left.
						nl := clamp(-(gx*0.55 + gy*0.83) * 2 / grad)
						spec := nl * nl * nl * nl * (1 - depth) * 0.5
						m := 0.12 + 0.74*depth
						cr := edge[0]*(1-m) + m + spec
						cg := edge[1]*(1-m) + m + spec
						cb := edge[2]*(1-m) + m + spec
						if markOn {
							if mk := sc.mark(fx-sc.shake, fy, core/12) * sc.markA * 0.85; mk > 0 {
								cr += (ink[0] - cr) * mk
								cg += (ink[1] - cg) * mk
								cb += (ink[2] - cb) * mk
							}
						}
						cr = cr*(1-fl) + fl
						cg = cg*(1-fl) + fl
						cb = cb*(1-fl) + fl
						r = clamp(cr)*ca + r*(1-ca)
						g = clamp(cg)*ca + g*(1-ca)
						b = clamp(cb)*ca + b*(1-ca)
						a = ca + a*(1-ca)
					}
					// Its glow.
					gl := float32(glowAmp * smooth(0.12, 1, f) * (1 - ca))
					er += gl * float32(glowTint[0])
					eg += gl * float32(glowTint[1])
					eb += gl * float32(glowTint[2])
				}
			}
			if er+eg+eb > 0.002 {
				er, eg, eb = 1-fexp(er), 1-fexp(eg), 1-fexp(eb)
				ea := float64(max(er, eg, eb))
				r = float64(er) + r*(1-ea)
				g = float64(eg) + g*(1-ea)
				b = float64(eb) + b*(1-ea)
				a = ea + a*(1-ea)
			}
			if a <= 0.001 {
				continue
			}
			i := pi * 4
			ab := byte(255*clamp(a) + 0.5)
			px[i+0] = min(ab, byte(255*clamp(b)+0.5))
			px[i+1] = min(ab, byte(255*clamp(g)+0.5))
			px[i+2] = min(ab, byte(255*clamp(r)+0.5))
			px[i+3] = ab
		}
	}
	return px
}
