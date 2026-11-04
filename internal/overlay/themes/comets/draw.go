// Package comets draws dictation's state as a thin, luminous ring of fine
// particles with a few bright comets
// streaking around it: the ring breathes with the voice while recording, the
// comets race while transcribing, orbit as one comet per message waiting for
// a busy agent, and spiral into one bright bloom when a message arrives.
package comets

import (
	"math"
	"math/rand"

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
	centre     = Canvas / 2.0
	baseRing   = 34.0 // the ring's radius at rest
	maxComets  = 7
	nParticles = 180
	histLen    = 40 // comet positions remembered for the trail
)

type rgb [3]float64

var (
	ice   = rgb{0.36, 0.82, 1.00}
	mint  = rgb{0.50, 1.00, 0.78}
	amber = rgb{1.00, 0.58, 0.26}
)

// particle is one grain of the ring: a fixed place on it with its own drift,
// sway and twinkle, so the ring shimmers without ever flickering.
type particle struct {
	ang    float64 // angle on the ring
	off    float64 // radial offset from the ring, in pixels at spread 1
	size   float64 // kernel radius in pixels
	gain   float64 // brightness
	drift  float64 // slow angular speed of its own
	swayF  float64 // radial sway frequency and phase
	swayPh float64
	twF    float64 // twinkle frequency and phase
	twPh   float64
}

var particles = func() []particle {
	r := rand.New(rand.NewSource(7))
	var clumps [7]float64
	for i := range clumps {
		clumps[i] = r.Float64() * 2 * math.Pi
	}
	ps := make([]particle, nParticles)
	for i := range ps {
		ang := r.Float64() * 2 * math.Pi
		if r.Float64() < 0.45 { // denser knots here and there, as in frost
			ang = clumps[r.Intn(len(clumps))] + r.NormFloat64()*0.28
		}
		off := r.NormFloat64() * 1.4
		if r.Float64() < 0.16 { // strays drifting further from the ring
			off = r.NormFloat64() * 5
		}
		size, gain := 0.95+0.45*r.Float64(), 0.45+0.7*r.Float64()
		if r.Float64() < 0.1 { // a few bright grains
			size, gain = size+0.5, gain+0.9
		}
		ps[i] = particle{
			ang: ang, off: off, size: size, gain: gain,
			drift:  (r.Float64() - 0.5) * 0.08,
			swayF:  0.3 + 0.5*r.Float64(),
			swayPh: r.Float64() * 2 * math.Pi,
			twF:    0.7 + 1.5*r.Float64(),
			twPh:   r.Float64() * 2 * math.Pi,
		}
	}
	return ps
}()

type pt struct{ x, y float64 }

// comet is one bright head with a trail of where it has been.
type comet struct {
	off   float64 // angle from the comets' shared phase, eased toward its slot
	orbit float64 // orbit radius as a fraction of the ring: 1 on it, 0 at the centre
	a     float64 // visibility 0..1
	hist  [histLen]pt
	histT [histLen]float64
	hn    int // positions remembered
	hi    int // where the next one goes
}

func (c *comet) remember(p pt, t float64) {
	c.hist[c.hi], c.histT[c.hi] = p, t
	c.hi = (c.hi + 1) % histLen
	if c.hn < histLen {
		c.hn++
	}
}

func (c *comet) forget() { c.hn, c.hi = 0, 0 }

// pose is where every eased quantity of the scene should be for a state.
type pose struct {
	ring   float64 // ring radius in pixels
	bright float64 // particle brightness
	spread float64 // how far the particles scatter from the ring
	omega  float64 // comet angular speed, radians per second
	halo   float64 // dark halo opacity
	bloom  float64 // centre glow intensity and radius
	bloomR float64
	tint   rgb
	n      int     // comets shown
	orbit  float64 // where the comets orbit: 1 on the ring, 0 at the centre
	cometA float64 // comet visibility
	trailT float64 // trail length in seconds
	g      float64 // whole-scene opacity: 0 leaves, 1 shows
}

// scene eases every quantity toward each state's pose, so every change of
// state flows: the ring swells, comets fade in, re-space, spiral and leave.
type scene struct {
	on    bool // appeared and not yet gone
	kind  Kind
	since float64 // seconds in kind
	clock float64 // seconds shown, in all
	level float64 // smoothed microphone level
	phase float64 // the comets' shared angle
	spin  float64 // the particle ring's slow turn
	shake float64 // Problem's shudder, decaying
	n     int     // comets last asked for (Done and Problem keep them)

	ring, bright, spread, omega, halo, bloom, bloomR float64
	tint                                             rgb
	orbit, trailT, scale, g                          float64
	fade                                             float64 // 0 gone .. 1 shown, moving linearly; g is its smooth curve
	ripple                                           float64 // Done's ripple, seconds since it started, or -1
	comets                                           [maxComets]comet

	light []float32 // render's additive rgb canvas, kept between frames
}

func (sc *scene) pose(kind Kind, s State) pose {
	p := pose{
		ring: baseRing, bright: 0.6, spread: 0.8, omega: 1.3, halo: 0.48,
		bloom: 0.05, bloomR: 9, tint: ice, orbit: 1, cometA: 1, trailT: 0.3, g: 1,
	}
	switch kind {
	case Recording:
		l := sc.level
		p.ring = baseRing - 1 + 6*l
		p.bright = 0.7 + 0.8*l
		p.spread = 1 + 2.4*l
		p.omega = 0.8 + 0.5*l
		p.n = 2
		p.trailT = 0.28
		p.bloom = 0.05 + 0.55*l
		p.bloomR = 7 + 9*l
	case Transcribing:
		p.ring = baseRing - 2
		p.bright = 0.45
		p.spread = 0.55
		p.omega = 4.8
		p.n = 3
		p.trailT = 0.34
		p.bloom = 0.08
	case Queued:
		p.n = max(1, min(s.Queued, maxComets))
		p.omega = 1.2 + 0.15*float64(p.n)
	case Done:
		// The comets spiral into the middle and become one bloom; a ripple
		// runs out through the ring, which brightens and settles.
		u := sc.since / 0.3
		p.n = sc.n
		p.orbit = 0
		p.omega = 5
		p.trailT = 0.32
		if sc.since > 0.42 {
			p.cometA = 0
		}
		p.bloom = 0.1 + 3.2*u*math.Exp(1-u)
		p.bloomR = 11 + 6*math.Min(1, u)
		p.bright = 0.6 + 0.6*math.Exp(-math.Max(0, sc.since-0.4)/0.6)
		p.spread = 0.6
		p.ring = baseRing + 1
		p.tint = mint
	case Problem:
		p.n = max(sc.n, 2)
		p.omega = -0.5
		p.trailT = 0.22
		p.spread = 2.4
		p.bright = 1.15 + 0.4*math.Sin(sc.clock*2*math.Pi*1.4)
		p.bloom = 0.45
		p.bloomR = 11
		p.tint = amber
		p.ring = baseRing - 1
	case Hidden:
		p.g, p.n = 0, 0
		p.tint, p.bloom = sc.tint, 0
	}
	return p
}

func ease(cur *float64, to, rate, dt float64) {
	*cur += (to - *cur) * (1 - math.Exp(-dt*rate))
}

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// step moves the scene a frame (dt seconds) toward kind's pose and reports
// whether anything is still visible.
func (sc *scene) step(kind Kind, s State, t, dt float64) bool {
	dt = math.Max(1e-3, math.Min(dt, 0.1))
	if kind == Hidden && !sc.on {
		return false
	}
	if kind != sc.kind {
		sc.kind, sc.since = kind, 0
		if kind == Problem {
			sc.shake = 1
		}
		if kind == Done {
			sc.ripple = 0
		}
	} else {
		sc.since += dt
	}
	sc.clock += dt
	// The level follows the voice quickly and lets go a little slower.
	rate := 10.0
	if s.Level > sc.level {
		rate = 28
	}
	sc.level += (s.Level - sc.level) * math.Min(1, dt*rate)
	p := sc.pose(kind, s)
	if !sc.on {
		// Appear: everything snaps to its pose, then grows and fades in.
		sc.on = true
		sc.ring, sc.bright, sc.spread, sc.omega = p.ring, p.bright, p.spread, p.omega
		sc.halo, sc.bloom, sc.bloomR, sc.tint = p.halo, p.bloom, p.bloomR, p.tint
		sc.orbit, sc.trailT = p.orbit, p.trailT
		sc.scale, sc.fade, sc.g, sc.ripple = 0.74, 0, 0, -1
		for i := range sc.comets {
			sc.comets[i] = comet{}
		}
	}
	if p.n > 0 {
		sc.n = p.n
	}
	ease(&sc.ring, p.ring, 7, dt)
	ease(&sc.bright, p.bright, 7, dt)
	ease(&sc.spread, p.spread, 6, dt)
	ease(&sc.omega, p.omega, 4, dt)
	ease(&sc.halo, p.halo, 6, dt)
	ease(&sc.bloom, p.bloom, 9, dt)
	ease(&sc.bloomR, p.bloomR, 7, dt)
	ease(&sc.orbit, p.orbit, 6, dt)
	ease(&sc.trailT, p.trailT, 4, dt)
	// Appearing takes 0.4 s, leaving 0.45 s, both easing in and out, and the
	// whole ring grows and shrinks a little with it.
	if p.g > sc.fade {
		sc.fade = math.Min(1, sc.fade+dt/0.4)
	} else {
		sc.fade = math.Max(0, sc.fade-dt/0.45)
	}
	sc.g = sc.fade * sc.fade * (3 - 2*sc.fade)
	sc.scale = 0.74 + 0.26*sc.g
	for j := range sc.tint {
		ease(&sc.tint[j], p.tint[j], 5, dt)
	}
	sc.shake *= math.Exp(-dt / 0.22)
	if sc.ripple >= 0 {
		sc.ripple += dt
	}
	sc.phase += sc.omega * dt
	sc.spin += 0.12 * dt

	cx, cy := sc.centre()
	for i := range sc.comets {
		c := &sc.comets[i]
		want := i < p.n
		slot := 2 * math.Pi * float64(i) / float64(max(1, p.n))
		if want && c.a < 0.01 {
			// Born at its slot on the ring, without a trail behind it.
			c.off, c.orbit = slot, 1
			c.forget()
		}
		if want {
			d := math.Mod(slot-c.off+3*math.Pi, 2*math.Pi) - math.Pi // the short way round
			ease(&c.off, c.off+d, 5, dt)
			ease(&c.orbit, sc.orbit, 6, dt)
			ease(&c.a, p.cometA, 8, dt)
		} else {
			ease(&c.a, 0, 8, dt)
		}
		if c.a > 0.005 {
			r := sc.ring * sc.scale * c.orbit
			ang := sc.phase + c.off
			c.remember(pt{cx + r*math.Cos(ang), cy + r*math.Sin(ang)}, sc.clock)
		}
	}
	if kind == Hidden && sc.fade <= 0 {
		sc.on = false
		return false
	}
	return true
}

// centre is where the ring is drawn: the middle, shuddering in Problem.
func (sc *scene) centre() (float64, float64) {
	return centre + 2.6*sc.shake*math.Sin(sc.clock*48), centre + 0.8*sc.shake*math.Sin(sc.clock*31)
}
