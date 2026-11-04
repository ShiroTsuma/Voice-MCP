// Package reference shows dictation's state in the bottom right corner of the
// screen as a ring of icy, glowing particles over a soft dark teal halo. The
// ring breathes with the voice while recording, a bright wave sweeps round it
// while transcribing, its particles gather into one star per message waiting
// for a busy agent, rush into the middle and burst when a message arrives, and
// turn amber and shiver on a problem. It never takes focus and lets clicks
// through.
package reference

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
	ringR  = 36.0 // the ring's radius in pixels
	nRing  = 200  // particles that make up the ring
	nDust  = 60   // loose sparks drifting inside and around it
	nParts = nRing + nDust
	// Each queued message takes this many ring particles for its star.
	perStar = (nRing + 6) / 7
)

type rgb [3]float64

var (
	icy      = rgb{0.58, 0.90, 1.00} // a faint particle
	white    = rgb{1.00, 1.00, 1.00} // a dense cluster
	teal     = rgb{0.02, 0.16, 0.21} // the halo
	amber    = rgb{1.00, 0.62, 0.20}
	cream    = rgb{1.00, 0.93, 0.78}
	darkRust = rgb{0.19, 0.07, 0.02}
)

// particle is one glowing dot: where it belongs on the ring (fixed when the
// scene is seeded) and where it is now.
type particle struct {
	home  float64 // angle on the ring the particle belongs to
	sigma float64 // how far from the ring it strays: more inside a clump
	g1    float64 // unit gaussians: radial offset and spread inside a star
	g2    float64
	dust  bool    // a loose spark rather than part of the ring
	dustR float64 // a spark's distance from the centre, as a fraction of ringR
	size  float64 // about 0.6 (a pinpoint) .. 1.7 (a bright bead)
	gain  float64 // brightness at rest
	phase float64 // twinkle
	freq  float64

	ang, rad, bri float64 // now
}

// scene eases its particles toward each state's layout, so every change of
// state flows: particles slide round the ring, gather, scatter and fade on
// the way.
type scene struct {
	ready   bool
	visible bool
	p       [nParts]particle

	level    float64 // smoothed microphone level
	presence float64 // how far the halo has faded in
	rot      float64 // the ring's rotation (radians) and its speed
	rotSpeed float64
	kind     Kind
	since    float64 // t when kind began
	tint     rgb     // colour of a faint particle
	hot      rgb     // colour of a dense cluster
	halo     rgb
	shake    float64 // sideways offset of the whole ring, pixels
	flare    float64 // a flash in the middle, Done only

	// Render scratch, allocated once.
	core, blur, wide, tmp []float32
	haloMask              []float32 // halo alpha per pixel, zero at the window's edge
	edge                  []float32 // fade to nothing at the window's edge
}

// rng is a small deterministic generator (splitmix64), so the ring's clumps
// look the same every time.
type rng uint64

func (r *rng) next() uint64 {
	*r += 0x9e3779b97f4a7c15
	z := uint64(*r)
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (r *rng) float() float64 { return float64(r.next()>>11) / (1 << 53) }

func (r *rng) gauss() float64 {
	u := 1 - r.float()
	return math.Sqrt(-2*math.Log(u)) * math.Cos(2*math.Pi*r.float())
}

// density is how crowded the ring is at angle a: a few overlapping waves
// make dense clumps and sparse gaps.
func density(a float64) float64 {
	v := 0.55*math.Sin(3*a+0.4) + 0.35*math.Sin(5*a+2.1) + 0.3*math.Sin(8*a+4.0) + 0.2*math.Sin(13*a+1.0)
	return math.Exp(1.7 * v)
}

func (sc *scene) init() {
	sc.ready = true
	sc.tint, sc.hot, sc.halo = icy, white, teal
	r := rng(0x5eed)
	maxD := 0.0
	for a := 0.0; a < 2*math.Pi; a += 0.001 {
		maxD = math.Max(maxD, density(a))
	}
	for i := range sc.p {
		p := &sc.p[i]
		p.g1, p.g2 = r.gauss(), r.gauss()
		p.phase = 2 * math.Pi * r.float()
		p.freq = 1.2 + 2.6*r.float()
		if i < nRing {
			for {
				p.home = 2 * math.Pi * r.float()
				if r.float()*maxD < density(p.home) {
					break
				}
			}
			d := density(p.home) / maxD
			p.sigma = 1.6 + 3.0*math.Pow(d, 0.7)
			u := r.float()
			p.size = 0.6 + 0.9*u*u
			if r.float() < 0.1 {
				p.size = 1.4 + 0.3*r.float()
			}
			p.gain = 0.55 + 0.45*r.float()
		} else {
			p.dust = true
			p.home = 2 * math.Pi * r.float()
			if r.float() < 0.55 {
				p.dustR = 0.15 + 0.7*r.float()
			} else {
				p.dustR = 1.15 + 0.45*r.float()
			}
			p.sigma = 2
			p.size = 0.5 + 0.4*r.float()
			p.gain = 0.25 + 0.4*r.float()
		}
		p.ang, p.rad = p.home, ringR*1.7
	}
	const c = Canvas
	sc.core = make([]float32, c*c)
	sc.blur = make([]float32, c*c)
	sc.wide = make([]float32, c*c)
	sc.tmp = make([]float32, c*c)
	sc.haloMask = make([]float32, c*c)
	sc.edge = make([]float32, c*c)
	for y := 0; y < c; y++ {
		for x := 0; x < c; x++ {
			d := math.Hypot(float64(x)+0.5-c/2, float64(y)+0.5-c/2)
			e := 1 - smooth(c/2-12, c/2-1.5, d)
			sc.edge[y*c+x] = float32(e)
			// A soft mist behind the ring; most of the halo's darkness comes
			// from the glow round the particles themselves, in render.
			h := 0.36 * math.Exp(-(d/40)*(d/40))
			sc.haloMask[y*c+x] = float32(h * e)
		}
	}
}

// scatter puts every particle far outside the ring and dark, so the next
// frames draw it drifting in.
func (sc *scene) scatter() {
	for i := range sc.p {
		p := &sc.p[i]
		p.ang = p.home + sc.rot
		p.rad = ringR * (1.6 + 0.3*p.g2)
		if p.dust {
			p.rad = ringR * (p.dustR + 0.6)
		}
		p.bri = 0
	}
}

func wrapAngle(a float64) float64 {
	a = math.Mod(a+math.Pi, 2*math.Pi)
	if a < 0 {
		a += 2 * math.Pi
	}
	return a - math.Pi
}

// behind is how far angle a trails the wave's head, 0..2π.
func behind(head, a float64) float64 {
	d := math.Mod(head-a, 2*math.Pi)
	if d < 0 {
		d += 2 * math.Pi
	}
	return d
}

func ease(dt, rate float64) float64 { return 1 - math.Exp(-dt*rate) }

// step moves the particles a frame (dt seconds) toward kind's layout and
// reports whether anything is still visible.
func (sc *scene) step(kind Kind, s State, t, dt float64) bool {
	if !sc.ready {
		sc.init()
	}
	if kind != sc.kind {
		sc.kind, sc.since = kind, t
	}
	if kind != Hidden && !sc.visible {
		sc.scatter()
	}
	tau := t - sc.since

	// Loudness: quick to rise, slower to settle.
	lk := dt * 8
	if s.Level > sc.level {
		lk = dt * 25
	}
	sc.level += (s.Level - sc.level) * math.Min(1, lk)

	want := 1.0
	if kind == Hidden {
		want = 0
	}
	if kind == Done {
		// The mist thins as the burst fades, so nothing lingers after it.
		want = clamp(1 - (tau-0.5)/0.8)
	}
	sc.presence += (want - sc.presence) * ease(dt, 5)

	speed := [...]float64{0.1, 0.18, 3.2, 0.35, 0.5, 0}[kind]
	sc.rotSpeed += (speed - sc.rotSpeed) * ease(dt, 3)
	sc.rot += sc.rotSpeed * dt

	tint, hot, halo := icy, white, teal
	if kind == Problem {
		tint, hot, halo = amber, cream, darkRust
	}
	ck := ease(dt, 6)
	for j := range tint {
		sc.tint[j] += (tint[j] - sc.tint[j]) * ck
		sc.hot[j] += (hot[j] - sc.hot[j]) * ck
		sc.halo[j] += (halo[j] - sc.halo[j]) * ck
	}

	sc.shake = 0
	if kind == Problem {
		sc.shake = 3.2 * math.Sin(tau*34) * math.Exp(-tau*2)
	}
	if kind == Done {
		x := (tau - 0.32) / 0.16
		sc.flare = math.Exp(-x * x)
	} else {
		sc.flare *= math.Exp(-dt * 8)
	}

	lvl := sc.level
	n := min(max(s.Queued, 1), 7)
	any := false
	for i := range sc.p {
		p := &sc.p[i]
		tw := 0.78 + 0.22*math.Sin(t*p.freq+p.phase)
		var ta, tr, tb float64 // targets
		ka, kb := ease(dt, 6), ease(dt, 6)
		switch kind {
		case Recording:
			ta = p.home + sc.rot + 0.03*math.Sin(t*0.9+p.phase)
			if p.dust {
				tr = ringR * p.dustR * (1 + 0.14*lvl)
				tb = p.gain * tw * (0.2 + 1.0*lvl)
			} else {
				tr = ringR*(1+0.14*lvl) + p.g1*p.sigma*(1+0.7*lvl)
				tb = p.gain * tw * (0.5 + 0.6*lvl)
			}
		case Transcribing:
			// One bright head with a long tail sweeps round a dim ring.
			ta = p.home + sc.rot*0.05
			wave := math.Exp(-behind(sc.rot, ta) / 1.1)
			if p.dust {
				tr = ringR * p.dustR
				tb = p.gain * tw * 0.15
			} else {
				tr = ringR + p.g1*p.sigma*0.55 + 3*wave
				tb = p.gain * (0.06 + 1.6*wave)
			}
		case Queued:
			if star := i / perStar; !p.dust && star < n {
				c := 2*math.Pi*float64(star)/float64(n) + sc.rot - math.Pi/2
				ta = c + p.g2*0.085
				tr = ringR + p.g1*2.2
				tb = p.gain * tw * 1.05
				ka = ease(dt, 4.5) // a graceful slide round the ring
			} else {
				ta = p.home + sc.rot
				tr = ringR + p.g1*p.sigma*0.6
				if p.dust {
					tr = ringR * p.dustR
				}
				tb = p.gain * tw * 0.16
			}
		case Done:
			ta = p.ang
			if tau < 0.3 {
				tr, tb = 0, 1.5*p.gain
				ka, kb = ease(dt, 11), ease(dt, 11)
			} else {
				tr = ringR * (1.6 + 0.3*math.Max(-1.5, math.Min(1.5, p.g2)))
				tb = 0
				ka, kb = ease(dt, 4.5), ease(dt, 3)
			}
		case Problem:
			pulse := 0.55 + 0.45*math.Abs(math.Cos(tau*7))
			ta = p.home + sc.rot + 0.06*math.Sin(t*7+p.phase*3)
			if p.dust {
				tr = ringR * p.dustR
				tb = p.gain * 0.5 * pulse
			} else {
				tr = ringR*0.97 + p.g1*p.sigma*1.4
				tb = p.gain * tw * pulse
			}
		default: // Hidden: drift outward and fade
			ta = p.home + sc.rot
			tr = ringR * 1.7
			if p.dust {
				tr = ringR * (p.dustR + 0.6)
			}
			tb = 0
			ka, kb = ease(dt, 3.5), ease(dt, 4)
		}
		p.ang += wrapAngle(ta-p.ang) * ka
		p.rad += (tr - p.rad) * ka
		p.bri += (tb - p.bri) * kb
		if p.bri > 0.02 {
			any = true
		}
	}
	sc.visible = sc.presence > 0.006 || any
	return sc.visible
}

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func smooth(e0, e1, v float64) float64 {
	t := clamp((v - e0) / (e1 - e0))
	return t * t * (3 - 2*t)
}
