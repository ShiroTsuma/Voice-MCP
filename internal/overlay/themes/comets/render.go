package comets

import "math"

// Light is drawn additively into a float canvas and tone-mapped: faint light
// keeps the tint, bright light saturates to white, so grains and comet heads
// glow icy. A dark halo sits under everything so the ring reads on light
// desktops too.

const toneSteps = 4096

var tone = func() [toneSteps + 1]float32 {
	var t [toneSteps + 1]float32
	for i := range t {
		t[i] = float32(1 - math.Exp(-float64(i)*toneMax/toneSteps))
	}
	return t
}()

const toneMax = 8.0

func tonemap(l float32) float32 {
	if l <= 0 {
		return 0
	}
	if l >= toneMax {
		return 1
	}
	return tone[int(l*(toneSteps/toneMax))]
}

// splat adds a soft dot of light: intensity i at (x, y), radius r, tinted c.
func (sc *scene) splat(x, y, r, i float64, c rgb) {
	if i <= 0 || r <= 0 {
		return
	}
	x0, x1 := int(math.Floor(x-r)), int(math.Ceil(x+r))
	y0, y1 := int(math.Floor(y-r)), int(math.Ceil(y+r))
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, Canvas-1), min(y1, Canvas-1)
	inv := 1 / (r * r)
	cr, cg, cb := float32(i*c[0]), float32(i*c[1]), float32(i*c[2])
	for py := y0; py <= y1; py++ {
		dy := float64(py) + 0.5 - y
		row := py * Canvas
		for px := x0; px <= x1; px++ {
			dx := float64(px) + 0.5 - x
			q := (dx*dx + dy*dy) * inv
			if q >= 1 {
				continue
			}
			w := float32((1 - q) * (1 - q))
			k := (row + px) * 3
			sc.light[k] += w * cr
			sc.light[k+1] += w * cg
			sc.light[k+2] += w * cb
		}
	}
}

// render draws the scene into a premultiplied BGRA canvas, top row first, as
// UpdateLayeredWindow wants it.
func (sc *scene) render() []byte {
	px := make([]byte, Canvas*Canvas*4)
	if !sc.on || sc.g <= 0 {
		return px
	}
	if sc.light == nil {
		sc.light = make([]float32, Canvas*Canvas*3)
	} else {
		clear(sc.light)
	}
	cx, cy := sc.centre()
	g, scale, tint := sc.g, sc.scale, sc.tint
	ring := sc.ring * scale

	// The grains of the ring.
	for i := range particles {
		p := &particles[i]
		ang := p.ang + sc.spin + p.drift*sc.clock
		sway := 0.5 * math.Sin(sc.clock*p.swayF+p.swayPh)
		rad := ring + (p.off*sc.spread+sway)*scale
		tw := 0.55 + 0.45*math.Sin(sc.clock*p.twF+p.twPh)
		sc.splat(cx+rad*math.Cos(ang), cy+rad*math.Sin(ang), p.size*scale, p.gain*sc.bright*tw*g, tint)
	}

	// The comets: a trail of where each has been, then a white-hot head.
	for i := range sc.comets {
		c := &sc.comets[i]
		if c.a <= 0.005 || c.hn == 0 {
			continue
		}
		a := c.a * g
		newest := (c.hi - 1 + histLen) % histLen
		head := c.hist[newest]
		now := c.histT[newest]
		for k := 1; k < c.hn; k++ {
			j0 := (newest - k + 1 + histLen) % histLen
			j1 := (newest - k + histLen) % histLen
			p0, p1 := c.hist[j0], c.hist[j1]
			age0, age1 := now-c.histT[j0], now-c.histT[j1]
			if age0 >= sc.trailT {
				break
			}
			steps := max(1, int(math.Hypot(p1.x-p0.x, p1.y-p0.y)/1.1))
			for s := 0; s < steps; s++ {
				u := float64(s) / float64(steps)
				age := age0 + (age1-age0)*u
				f := 1 - age/sc.trailT
				if f <= 0 {
					break
				}
				sc.splat(p0.x+(p1.x-p0.x)*u, p0.y+(p1.y-p0.y)*u, (0.7+1.9*f)*scale, 0.55*f*f*a, tint)
			}
		}
		sc.splat(head.x, head.y, 8*scale, 0.33*a, tint)
		sc.splat(head.x, head.y, 2.7*scale, 2.6*a, tint)
	}

	// The bloom in the middle (the voice while recording, success when done):
	// a bright heart inside a wide, faint glow.
	sc.splat(cx, cy, sc.bloomR*0.55*scale, sc.bloom*0.7*g, tint)
	sc.splat(cx, cy, sc.bloomR*1.4*scale, sc.bloom*0.22*g, tint)

	// A ripple running out through the ring after delivery.
	rippleR, rippleA := 0.0, 0.0
	if sc.ripple >= 0.3 && sc.ripple < 1.3 {
		u := (sc.ripple - 0.3) / 1.0
		rippleR = ring * (0.6 + 1.3*u)
		rippleA = 0.9 * (1 - u) * (1 - u) * g
	}

	// Compose: light on top of the dark halo, premultiplied.
	haloA := sc.halo * g
	discR := math.Min(62, 1.75*ring)
	const haloR, haloG, haloB = 0.03, 0.07, 0.14
	for y := 0; y < Canvas; y++ {
		dy := float64(y) + 0.5 - cy
		for x := 0; x < Canvas; x++ {
			dx := float64(x) + 0.5 - cx
			d := math.Sqrt(dx*dx + dy*dy)
			k := (y*Canvas + x) * 3
			lr, lg, lb := sc.light[k], sc.light[k+1], sc.light[k+2]
			if rippleA > 0 {
				w := (d - rippleR) / 3.5
				if w > -1 && w < 1 {
					w = (1 - w*w) * (1 - w*w)
					lr += float32(rippleA * w * tint[0])
					lg += float32(rippleA * w * tint[1])
					lb += float32(rippleA * w * tint[2])
				}
			}
			var h float64
			if d < discR {
				q := d / discR
				disc := (1 - q*q) * (1 - q*q)
				// Mostly a soft band under the ring; the middle stays nearly clear.
				band := (d - ring) / 9
				band = math.Exp(-band * band)
				h = haloA * (0.16*disc + 0.84*band)
			}
			if lr == 0 && lg == 0 && lb == 0 && h == 0 {
				continue
			}
			r, gg, b := tonemap(lr), tonemap(lg), tonemap(lb)
			la := float64(max(r, gg, b))
			hb := h * (1 - la)
			a := la + hb
			i := (y*Canvas + x) * 4
			px[i] = byte(255*(float64(b)+haloB*hb) + 0.5)
			px[i+1] = byte(255*(float64(gg)+haloG*hb) + 0.5)
			px[i+2] = byte(255*(float64(r)+haloR*hb) + 0.5)
			px[i+3] = byte(255*a + 0.5)
		}
	}
	return px
}
