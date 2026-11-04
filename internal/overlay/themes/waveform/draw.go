// Package waveform shows dictation's state in the bottom right corner of the
// screen as a ring of fine glowing particles, like a voice assistant's
// waveform bent into a circle: while recording, several frequency bands
// travel around the ring and displace it with the voice; while transcribing,
// three comets chase round it; waiting messages sit on it as pearls that fly
// together into a check mark when one is delivered; a problem shakes it amber
// with an exclamation mark inside. It never takes focus and lets clicks
// through.
package waveform

import (
	"math"

	"voice/internal/overlay/look"
)

// The shared states, under the names this theme's code uses.
type (
	Kind  = look.Kind
	State = look.State
)

const (
	Hidden       = look.Hidden
	Recording    = look.Recording
	Transcribing = look.Transcribing
	Queued       = look.Queued
	Done         = look.Done
	Problem      = look.Problem
)

// New starts the theme's scene.
func New() look.Scene { return &theme{} }

type theme struct{ sc scene }

func (t *theme) Size() int { return Canvas }
func (t *theme) Step(kind Kind, s State, now, dt float64) bool {
	return t.sc.step(kind, s, now, dt)
}
func (t *theme) Render() []byte { return t.sc.render() }

// Canvas is the window's size in pixels (square).
const Canvas = 128

const (
	centre    = Canvas / 2.0
	ringR     = 34.0 // the ring's resting radius
	ringN     = 176  // particles on the ring
	dustN     = 48   // loose particles drifting around it
	nDots     = ringN + dustN
	maxPearls = 8
)

type rgb [3]float64

var (
	cyan     = rgb{0.40, 0.86, 1.00}
	ice      = rgb{0.86, 0.97, 1.00}
	lavender = rgb{0.72, 0.66, 1.00}
	blue     = rgb{0.48, 0.70, 1.00}
	mint     = rgb{0.48, 1.00, 0.74}
	amber    = rgb{1.00, 0.62, 0.24}
)

func mix(a, b rgb, t float64) rgb {
	var c rgb
	for i := range c {
		c[i] = a[i] + (b[i]-a[i])*t
	}
	return c
}

// dot is one particle: where it is now, and the traits it was born with.
type dot struct {
	x, y, b float64 // pixels from the centre, brightness
	sz      float64 // gaussian sigma in pixels
	th      float64 // home angle on the ring
	r0      float64 // home radius (dust)
	jr      float64 // radial scatter, about -1..1
	tw, ph  float64 // twinkle rate and phase
	drift   float64 // angular drift, radians per second (dust)
	seed    float64 // 0..1
}

// pearl is one waiting message: a bigger, brighter particle.
type pearl struct{ x, y, b float64 }

// scene eases every particle toward where the current state wants it, so
// each change of state is a flight: the ring collapses into a check mark,
// pearls arrive from the middle, the whole thing blooms out of the centre
// when it appears and dissolves outward when it goes.
type scene struct {
	dots   [nDots]dot
	pearls [maxPearls]pearl
	tx, ty [nDots]float64 // targets this frame
	tb     [nDots]float64
	ptx    [maxPearls]float64
	pty    [maxPearls]float64
	ptb    [maxPearls]float64

	col      rgb     // current colour
	halo     float64 // current strength of the dark halo
	gain     float64 // current brightness multiplier
	presence float64 // 0 gone .. 1 shown
	level    float64 // microphone level, fast attack, slow release
	env      float64 // slower envelope of the level

	kind  Kind
	enter float64 // t when kind began
	ready bool
	gone  bool // nothing shown; the next state blooms from the centre

	// render buffers, allocated once
	light, glow, tmp []float32
	out              []byte
	haloMask         []float32
}

// xorshift gives the particles their traits, the same every run.
type xorshift uint64

func (x *xorshift) next() float64 {
	v := uint64(*x)
	v ^= v << 13
	v ^= v >> 7
	v ^= v << 17
	*x = xorshift(v)
	return float64(v>>11) / float64(1<<53)
}

// traitSeed picks the particles' traits: chosen so the scatter is even round
// the ring, without a clump that would read as a feature.
const traitSeed = 0x1cc7258cf3b357aa

func (sc *scene) setup() {
	sc.ready, sc.gone = true, true
	rnd := xorshift(traitSeed)
	for i := 0; i < ringN; i++ {
		d := &sc.dots[i]
		d.th = 2*math.Pi*(float64(i)+0.6*(rnd.next()-0.5))/ringN - math.Pi/2
		d.jr = rnd.next() + rnd.next() - 1 // triangular: most near the ring
		if rnd.next() < 0.08 {
			d.jr *= 1.8 // a few strays
		}
		u := rnd.next()
		d.sz = 0.8 + 0.55*u*u
		if rnd.next() < 0.07 {
			d.sz = 1.5
		}
		d.tw = 1.5 + 4*rnd.next()
		d.ph = 2 * math.Pi * rnd.next()
		d.seed = rnd.next()
	}
	for i := ringN; i < nDots; i++ {
		d := &sc.dots[i]
		d.th = 2 * math.Pi * rnd.next()
		if rnd.next() < 0.5 {
			d.r0 = ringR - 6 - 14*rnd.next()
		} else {
			d.r0 = ringR + 6 + 14*rnd.next()
		}
		d.jr = rnd.next() + rnd.next() - 1
		d.sz = 0.7 + 0.4*rnd.next()
		d.tw = 1 + 3*rnd.next()
		d.ph = 2 * math.Pi * rnd.next()
		d.drift = (rnd.next() - 0.5) * 0.5
		d.seed = rnd.next()
	}
	sc.col, sc.halo, sc.gain = cyan, 0.4, 1
}

func twinkle(d *dot, t float64) float64 {
	return 0.5 + 0.5*math.Sin(d.tw*t+d.ph)
}

// bands are the frequency bands travelling round the ring while recording:
// cycles round the ring, angular speed, amplitude when quiet, extra amplitude
// at full level, and phase.
var bands = [...]struct{ n, w, q, loud, ph float64 }{
	{2, 1.3, 1.0, 3.2, 0.0},
	{3, -2.1, 0.7, 3.2, 1.1},
	{5, 3.4, 0.45, 2.2, 2.3},
	{8, -5.0, 0.25, 1.0, 0.7},
	{13, 7.3, 0.12, 0.4, 3.9},
}

// wave is the ring's radial displacement at angle th: the bands added up.
func wave(th, t, level float64) float64 {
	var d float64
	for _, b := range bands {
		d += (b.q + level*b.loud) * math.Sin(b.n*th+b.w*t+b.ph)
	}
	return d
}

func frac(v float64) float64 { return v - math.Floor(v) }

// angdiff is the smallest angle between a and b.
func angdiff(a, b float64) float64 {
	d := math.Mod(a-b+math.Pi, 2*math.Pi)
	if d < 0 {
		d += 2 * math.Pi
	}
	return math.Abs(d - math.Pi)
}

// layout fills the targets for kind at time t and returns the colour, halo
// strength and brightness it wants.
func (sc *scene) layout(kind Kind, s State, t float64) (col rgb, halo, gain float64) {
	tau := t - sc.enter
	for j := range sc.ptx { // pearls hide in the middle unless placed
		sc.ptx[j], sc.pty[j], sc.ptb[j] = 0, 0, 0
	}
	switch kind {
	case Recording:
		l := sc.level
		spread := 1.4 + 1.8*l
		for i := 0; i < ringN; i++ {
			d := &sc.dots[i]
			th := d.th + 0.12*t
			r := ringR + 4*l + wave(th, t, l) + d.jr*spread
			sc.tx[i], sc.ty[i] = r*math.Cos(th), r*math.Sin(th)
			sc.tb[i] = (0.5 + 0.5*twinkle(d, t)) * (0.7 + 0.5*l)
		}
		for i := ringN; i < nDots; i++ {
			d := &sc.dots[i]
			th := d.th + d.drift*t
			out := 1.0
			if d.r0 < ringR {
				out = -1
			}
			r := d.r0 + 0.5*wave(th, t, l) + 8*l*out
			sc.tx[i], sc.ty[i] = r*math.Cos(th), r*math.Sin(th)
			sc.tb[i] = 0.14 + 0.2*twinkle(d, t) + 0.2*l
		}
		return mix(cyan, ice, 0.45*sc.env), 0.4 + 0.12*sc.env, 1
	case Transcribing:
		const spin = 2.8
		for i := 0; i < ringN; i++ {
			d := &sc.dots[i]
			ph := 3 * (d.th - spin*t)
			th := d.th - 0.28*math.Sin(ph) // bunch into three comets
			c := math.Cos(ph)
			r := ringR - 0.5 + 1.2*c + d.jr*1.4
			sc.tx[i], sc.ty[i] = r*math.Cos(th), r*math.Sin(th)
			sc.tb[i] = 0.22 + 0.78*math.Max(0, c)*math.Max(0, c)
		}
		for i := ringN; i < nDots; i++ {
			d := &sc.dots[i]
			u := frac(t*0.45 + d.seed) // spiral inward, fading at both ends
			th := d.th + 1.8*t + 2*u
			r := ringR + 10 - 28*u
			sc.tx[i], sc.ty[i] = r*math.Cos(th), r*math.Sin(th)
			s := math.Sin(math.Pi * u)
			sc.tb[i] = 0.4 * s * s
		}
		return lavender, 0.42, 1
	case Queued:
		n := max(1, min(s.Queued, maxPearls-1))
		spin := 0.35 * t
		for j := 0; j < n; j++ {
			th := -math.Pi/2 + 2*math.Pi*float64(j)/float64(n) + spin
			sc.ptx[j], sc.pty[j] = ringR*math.Cos(th), ringR*math.Sin(th)
			sc.ptb[j] = 1.8 + 0.25*math.Sin(3*t+float64(j))
		}
		for i := 0; i < ringN; i++ {
			d := &sc.dots[i]
			th := d.th + spin
			near := 0.0
			for j := 0; j < n; j++ {
				a := angdiff(th, -math.Pi/2+2*math.Pi*float64(j)/float64(n)+spin) / 0.26
				near += math.Exp(-a * a)
			}
			r := ringR + 0.8*math.Sin(2*th+1.1*t) + d.jr*1.5
			sc.tx[i], sc.ty[i] = r*math.Cos(th), r*math.Sin(th)
			sc.tb[i] = 0.26 + 0.1*twinkle(d, t) + 0.3*near
		}
		for i := ringN; i < nDots; i++ {
			d := &sc.dots[i]
			th := d.th + d.drift*t
			sc.tx[i], sc.ty[i] = d.r0*math.Cos(th), d.r0*math.Sin(th)
			sc.tb[i] = 0.1 + 0.1*twinkle(d, t)
		}
		return blue, 0.45, 1
	case Done:
		// The ring unrolls onto a check mark; pearls fly to its corner and
		// are absorbed there in a flare.
		const ax, ay, bx, by, cx, cy = -15, 1, -5, 11, 17, -13
		lab := math.Hypot(bx-ax, by-ay)
		lbc := math.Hypot(cx-bx, cy-by)
		for i := 0; i < ringN; i++ {
			d := &sc.dots[i]
			u := (float64(i) + 0.5) / ringN * (lab + lbc)
			var x, y, nx, ny float64
			if u < lab {
				f := u / lab
				x, y = ax+(bx-ax)*f, ay+(by-ay)*f
				nx, ny = -(by-ay)/lab, (bx-ax)/lab
			} else {
				f := (u - lab) / lbc
				x, y = bx+(cx-bx)*f, by+(cy-by)*f
				nx, ny = -(cy-by)/lbc, (cx-bx)/lbc
			}
			sc.tx[i], sc.ty[i] = x+nx*d.jr*1.3, y+ny*d.jr*1.3
			sc.tb[i] = 0.75 + 0.25*twinkle(d, t)
		}
		for i := ringN; i < nDots; i++ { // a shockwave of dust
			d := &sc.dots[i]
			th := d.th + d.drift*t
			r := d.r0 + 26*(1-math.Exp(-3*tau))
			sc.tx[i], sc.ty[i] = r*math.Cos(th), r*math.Sin(th)
			sc.tb[i] = 0.5 * math.Exp(-2.5*tau)
		}
		pb := 1.3
		if tau > 0.25 {
			pb *= math.Exp(-6 * (tau - 0.25))
		}
		for j := range sc.ptx {
			sc.ptx[j], sc.pty[j], sc.ptb[j] = bx, by, pb
		}
		return mint, 0.5, 1 + 0.5*math.Exp(-4*tau)
	case Problem:
		shake := 3 * math.Sin(45*tau) * math.Exp(-4*tau)
		nerve := 0.4 + 0.6*math.Exp(-2*tau)
		for i := 0; i < ringN; i++ {
			d := &sc.dots[i]
			r := ringR + d.jr*1.8 + 2.2*nerve*math.Sin(23*d.th+31*t+d.ph)
			sc.tx[i], sc.ty[i] = r*math.Cos(d.th)+shake, r*math.Sin(d.th)
			sc.tb[i] = 0.45 + 0.35*(0.5+0.5*math.Sin(6*d.tw*t+d.ph))
		}
		for i := ringN; i < nDots; i++ { // an exclamation mark
			d := &sc.dots[i]
			k := i - ringN
			if k < 34 {
				u := float64(k) / 33
				sc.tx[i] = float64(k%2)*1.6 - 0.8 + 0.5*(d.seed-0.5) + shake
				sc.ty[i] = -18 + 25*u
			} else {
				a := 2 * math.Pi * d.seed
				rr := 2.2 * math.Sqrt(frac(d.seed*7.7))
				sc.tx[i], sc.ty[i] = rr*math.Cos(a)+shake, 14+rr*math.Sin(a)
			}
			sc.tb[i] = 1
		}
		return amber, 0.5, 1 + 0.4*math.Exp(-5*tau)
	}
	return sc.col, sc.halo, sc.gain
}

// step moves the particles a frame (dt seconds) toward kind's layout and
// reports whether anything is still visible.
func (sc *scene) step(kind Kind, s State, t, dt float64) bool {
	if !sc.ready {
		sc.setup()
	}
	if kind != sc.kind {
		sc.kind, sc.enter = kind, t
	}
	if s.Level > sc.level {
		sc.level += (s.Level - sc.level) * min(1, dt*30)
	} else {
		sc.level += (s.Level - sc.level) * min(1, dt*6)
	}
	sc.env += (sc.level - sc.env) * min(1, dt*4)
	if kind == Hidden {
		if sc.gone {
			return false
		}
		// Dissolve: drift outward, dim, and fade.
		sc.presence -= sc.presence * min(1, dt*4)
		grow := 1 + 0.7*dt
		dim := 1 - min(1, dt*3)
		for i := range sc.dots {
			d := &sc.dots[i]
			d.x, d.y, d.b = d.x*grow, d.y*grow, d.b*dim
		}
		for j := range sc.pearls {
			sc.pearls[j].b *= dim
		}
		if sc.presence < 0.02 {
			sc.gone = true
			return false
		}
		return true
	}
	if sc.gone {
		// Bloom out of the centre.
		sc.gone, sc.presence = false, 0
		rnd := xorshift(uint64(t*1000) + 7)
		for i := range sc.dots {
			d := &sc.dots[i]
			a, r := 2*math.Pi*rnd.next(), 4*rnd.next()
			d.x, d.y, d.b = r*math.Cos(a), r*math.Sin(a), 0
		}
		for j := range sc.pearls {
			sc.pearls[j] = pearl{}
		}
		sc.col, sc.halo, sc.gain = sc.layout(kind, s, t)
	}
	sc.presence += (1 - sc.presence) * min(1, dt*7)
	col, halo, gain := sc.layout(kind, s, t)
	k := 1 - math.Exp(-dt*16)
	kc := 1 - math.Exp(-dt*10)
	for i := range sc.dots {
		d := &sc.dots[i]
		d.x += (sc.tx[i] - d.x) * k
		d.y += (sc.ty[i] - d.y) * k
		d.b += (sc.tb[i] - d.b) * k
	}
	for j := range sc.pearls {
		p := &sc.pearls[j]
		p.x += (sc.ptx[j] - p.x) * k
		p.y += (sc.pty[j] - p.y) * k
		p.b += (sc.ptb[j] - p.b) * k
	}
	for i := range sc.col {
		sc.col[i] += (col[i] - sc.col[i]) * kc
	}
	sc.halo += (halo - sc.halo) * kc
	sc.gain += (gain - sc.gain) * kc
	return true
}
