// Package swarm shows dictation's state in the bottom right corner of the
// screen as a swarm of fireflies: a hundred and fifty glowing motes that drift
// on a curl-noise breeze while a soft spring holds them in a shape. The shape
// is what tells the state: a breathing ring that swells with the voice while
// recording, a comet chasing its tail while transcribing, one little flock per
// waiting message, a bright knot that blooms into a ring when a message
// arrives, and an amber, shaking ring around a dot when something went wrong.
// Every mote twinkles on its own. It never takes focus and lets clicks through.
package swarm

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
const Canvas = 144

// motes is how many fireflies there are.
const motes = 150

// ring is the radius of the resting ring, in pixels from the centre.
const ring = 31.0

type rgb [3]float64

// mote is one firefly.
type mote struct {
	x, y, vx, vy float64 // pixels from the centre; velocity in pixels per second
	bright       float64 // eased brightness the shape asks for
	lum          float64 // brightness this frame, twinkle included
	u, seed      float64 // where it sits in the shape, and a second lot, both 0..1
	size         float64 // 0.6 (a speck) .. 1.6 (a fat glow)
	phase, rate  float64 // twinkle phase and speed (radians, radians per second)
	white        float64 // how white its core is against the tint, 0..1
}

// scene holds the swarm and the eased, continuous bits of its state, so every
// change of kind is a flight from one shape to the next rather than a cut.
type scene struct {
	p     [motes]mote
	rng   *rand.Rand
	ready bool
	on    bool    // the swarm has flown in and not yet faded out
	life  float64 // 0 gone .. 1 fully shown
	kind  Kind
	since float64 // seconds in the current kind
	level float64 // smoothed microphone level
	col   rgb     // current tint
	glow  float64 // extra brightness, for Done's bloom
	shake float64 // sideways offset of the whole swarm, for Problem
	// Render buffers, reused between frames.
	light []float32
	dark  []float32
}

var (
	ice      = rgb{0.50, 0.90, 1.00}
	lavender = rgb{0.72, 0.72, 1.00}
	azure    = rgb{0.45, 0.72, 1.00}
	mint     = rgb{0.50, 1.00, 0.78}
	amber    = rgb{1.00, 0.68, 0.28}
)

func tint(k Kind) (rgb, bool) {
	switch k {
	case Recording:
		return ice, true
	case Transcribing:
		return lavender, true
	case Queued:
		return azure, true
	case Done:
		return mint, true
	case Problem:
		return amber, true
	}
	return rgb{}, false
}

func (sc *scene) init() {
	sc.rng = rand.New(rand.NewSource(7))
	sc.col = ice
	perm := sc.rng.Perm(motes)
	for i := range sc.p {
		p := &sc.p[i]
		p.u = (float64(perm[i]) + sc.rng.Float64()) / motes
		p.seed = sc.rng.Float64()
		r := sc.rng.Float64()
		p.size = 0.6 + 1.0*r*r
		p.phase = sc.rng.Float64() * 2 * math.Pi
		p.rate = 1.2 + 4.5*sc.rng.Float64()
		p.white = sc.rng.Float64()
	}
	sc.scatter()
	sc.ready = true
}

// scatter puts the motes out past the ring so they fly in when they appear.
func (sc *scene) scatter() {
	for i := range sc.p {
		p := &sc.p[i]
		a := sc.rng.Float64() * 2 * math.Pi
		r := 46 + 20*sc.rng.Float64()
		p.x, p.y = r*math.Cos(a), r*math.Sin(a)
		p.vx, p.vy = -p.x*0.6, -p.y*0.6
		p.bright, p.lum = 0.2, 0
	}
}

// free motes are the ones that leave the ring to wander inside it.
func (p *mote) free() bool { return p.seed < 0.2 }

// anchor is where mote i should hover for kind at time t, and how bright it
// should be there.
func (sc *scene) anchor(i int, kind Kind, s State, t float64) (ax, ay, bright float64) {
	p := &sc.p[i]
	dir := 1.0
	if p.seed > 0.6 {
		dir = -1
	}
	switch kind {
	case Recording:
		lv := sc.level
		if p.free() {
			// Sparks inside the ring, lit by the voice.
			r := 5 + 19*p.seed*5
			a := p.u*2*math.Pi + dir*t*(0.5+0.8*p.seed*5)
			return r * math.Cos(a), r * math.Sin(a), 0.12 + 1.1*lv
		}
		a := p.u*2*math.Pi + t*0.3
		w := (p.seed - 0.6) / 0.4 // -1..1 across the ring's thickness
		r := ring + 10*lv + w*(2.5+4*lv) + 3*lv*math.Sin(5*a+t*9)
		return r * math.Cos(a), r * math.Sin(a), 0.7 + 0.6*lv
	case Transcribing:
		if p.free() {
			r := 6 + 14*p.seed*5
			a := p.u*2*math.Pi + dir*t*0.4
			return r * math.Cos(a), r * math.Sin(a), 0.1
		}
		k := p.u * p.u // bunched toward the head
		a := -t*4.2 + k*2*math.Pi*0.82
		w := (p.seed - 0.6) / 0.4
		r := ring + w*(1.5+5*k)
		return r * math.Cos(a), r * math.Sin(a), 0.15 + 1.25*math.Pow(1-k, 1.5)
	case Queued:
		n := max(1, min(s.Queued, 7))
		c := i % n
		ca := 2*math.Pi*float64(c)/float64(n) - math.Pi/2 + t*0.45
		cr := 27.0
		r := 7.5 * math.Sqrt(p.seed)
		a := p.u*2*math.Pi + dir*t*1.6
		return cr*math.Cos(ca) + r*math.Cos(a), cr*math.Sin(ca) + r*math.Sin(a), 0.85
	case Done:
		e := sc.since - 0.35
		if e < 0 {
			// Everything meets in a knot.
			r := 4 * math.Sqrt(p.seed)
			a := p.u*2*math.Pi + dir*t*3
			return r * math.Cos(a), r * math.Sin(a), 1.3
		}
		// Then blooms into a ring, bright at first.
		grow := 1 - math.Exp(-e*4.5)
		a := p.u*2*math.Pi + t*0.3
		w := (p.seed - 0.5) * 2
		r := (ring+1)*grow + w*2.5*grow
		return r * math.Cos(a), r * math.Sin(a), 0.5 + 1.0*math.Exp(-e*1.5)
	case Problem:
		if p.free() {
			r := 3.5 * math.Sqrt(p.seed*5)
			a := p.u*2*math.Pi + dir*t*2.5
			return r * math.Cos(a), r * math.Sin(a), 1.1
		}
		a := p.u*2*math.Pi + t*0.2
		w := (p.seed - 0.6) / 0.4
		r := ring - 1 + w*3
		return r * math.Cos(a), r * math.Sin(a), 0.95
	}
	// Hidden: fly outward, away from the middle.
	d := math.Hypot(p.x, p.y) + 1e-6
	return p.x / d * 90, p.y / d * 90, 0.2
}

// dynamics is how hard the shape holds the swarm, how fast motion dies down
// and how strong the breeze is, for kind.
func (sc *scene) dynamics(kind Kind) (spring, damp, breeze float64) {
	switch kind {
	case Recording:
		return 32, 5.5, 40 + 150*sc.level
	case Transcribing:
		return 40, 6, 55
	case Queued:
		return 34, 5.5, 45
	case Done:
		if sc.since < 0.35 {
			return 70, 7, 30
		}
		return 36, 5.5, 50
	case Problem:
		return 45, 5, 70
	}
	return 10, 2.5, 90
}

// step moves the swarm a frame (dt seconds) toward kind's shape and reports
// whether anything is still visible.
func (sc *scene) step(kind Kind, s State, t, dt float64) bool {
	if !sc.ready {
		sc.init()
	}
	dt = math.Max(0, math.Min(dt, 0.1))
	if kind != sc.kind {
		sc.kind, sc.since = kind, 0
	} else {
		sc.since += dt
	}
	if kind == Recording {
		sc.level += (s.Level - sc.level) * (1 - math.Exp(-dt*16))
	} else {
		sc.level *= math.Exp(-dt * 6)
	}
	if kind != Hidden && !sc.on {
		sc.scatter()
		sc.on = true
	}
	target, rate := 1.0, 6.0
	if kind == Hidden {
		target, rate = 0, 4.5
	}
	sc.life += (target - sc.life) * (1 - math.Exp(-dt*rate))
	if kind == Hidden && sc.life < 0.01 {
		sc.on = false
		sc.life = 0
		return false
	}
	if c, ok := tint(kind); ok {
		k := 1 - math.Exp(-dt*6)
		for j := range sc.col {
			sc.col[j] += (c[j] - sc.col[j]) * k
		}
	}
	glow := 0.0
	if kind == Done && sc.since > 0.35 {
		glow = 0.9 * math.Exp(-(sc.since-0.35)*3)
	}
	sc.glow += (glow - sc.glow) * (1 - math.Exp(-dt*12))

	// Problem shakes the whole swarm sideways, a "no" that dies down; the
	// shake moves every mote as one so the ring holds its shape while it shakes.
	shake := 0.0
	if kind == Problem {
		shake = 7 * math.Sin(sc.since*24) * math.Exp(-sc.since*2.2)
	}
	dshake := shake - sc.shake
	sc.shake = shake

	spring, damp, breeze := sc.dynamics(kind)
	decay := math.Exp(-damp * dt)
	ease := 1 - math.Exp(-dt*8)
	for i := range sc.p {
		p := &sc.p[i]
		ax, ay, b := sc.anchor(i, kind, s, t)
		p.bright += (b - p.bright) * ease
		// Twinkle: a slow glimmer with the odd sharp flash.
		sn := 0.5 + 0.5*math.Sin(p.phase+t*p.rate)
		s3 := sn * sn * sn
		p.lum = p.bright * (0.4 + 0.5*s3 + 0.6*s3*s3*s3*s3)
		cx, cy := curl(p.x*0.045+p.seed*9, p.y*0.045, t*0.35+p.seed)
		p.vx += (spring*(ax-p.x) + breeze*cx) * dt
		p.vy += (spring*(ay-p.y) + breeze*cy) * dt
		p.vx *= decay
		p.vy *= decay
		p.x += p.vx*dt + dshake
		p.y += p.vy * dt
	}
	return true
}

// noise is smooth value noise in three dimensions, 0..1.
func noise(x, y, z float64) float64 {
	x0, y0, z0 := math.Floor(x), math.Floor(y), math.Floor(z)
	fx, fy, fz := x-x0, y-y0, z-z0
	fx, fy, fz = fx*fx*(3-2*fx), fy*fy*(3-2*fy), fz*fz*(3-2*fz)
	ix, iy, iz := int(x0), int(y0), int(z0)
	lerp := func(a, b, t float64) float64 { return a + (b-a)*t }
	c00 := lerp(hash(ix, iy, iz), hash(ix+1, iy, iz), fx)
	c10 := lerp(hash(ix, iy+1, iz), hash(ix+1, iy+1, iz), fx)
	c01 := lerp(hash(ix, iy, iz+1), hash(ix+1, iy, iz+1), fx)
	c11 := lerp(hash(ix, iy+1, iz+1), hash(ix+1, iy+1, iz+1), fx)
	return lerp(lerp(c00, c10, fy), lerp(c01, c11, fy), fz)
}

func hash(x, y, z int) float64 {
	h := uint32(x)*0x8da6b343 ^ uint32(y)*0xd8163841 ^ uint32(z)*0xcb1ab31f
	h ^= h >> 13
	h *= 0x5bd1e995
	h ^= h >> 15
	return float64(h&0xffffff) / float64(0xffffff)
}

// curl is a divergence-free breeze: the curl of the noise field, so motes swirl
// around each other instead of bunching up.
func curl(x, y, z float64) (float64, float64) {
	const e = 0.02
	dx := (noise(x+e, y, z) - noise(x-e, y, z)) / (2 * e)
	dy := (noise(x, y+e, z) - noise(x, y-e, z)) / (2 * e)
	return dy, -dx
}
