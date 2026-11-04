// Package torus shows dictation's state in the bottom right corner of the
// screen as a ring of glowing particles turning slowly in 3D, on a soft dark
// halo: the ring ripples and brightens with the voice while recording, spins
// up with a bright comet while transcribing, gathers into one cluster per
// message waiting for a busy agent, and pulls everything into one point that
// opens into a check mark when a message arrives. It never takes focus and
// lets clicks through.
package torus

import (
	"math"
	"math/rand/v2"

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
	nParticles = 360
	glyphEvery = 5 // every fifth particle may leave the ring to draw a glyph
	nGlyph     = nParticles / glyphEvery
	maxQueued  = 8
	merge      = 0.5 // seconds Done spends pulling everything into one point
)

type vec3 [3]float64
type rgb [3]float64

var (
	icy      = rgb{0.42, 0.86, 1.00}
	lavender = rgb{0.66, 0.58, 1.00}
	azure    = rgb{0.32, 0.58, 1.00}
	mint     = rgb{0.34, 1.00, 0.62}
	amber    = rgb{1.00, 0.56, 0.16}
)

// particle is one point of light. The fixed part places it on the torus and
// gives it its character; pos and b follow the current state's layout.
type particle struct {
	u, v, rr float64 // angle round the ring, angle round the tube, share of the tube's radius
	drift    float64 // its own slow flow along the ring, rad/s
	bright   float64
	size     float64 // core radius (gaussian sigma) in pixels at the ring's depth
	tw, ph   float64 // twinkle speed and phase
	w, p     vec3    // wander speeds and phases
	dir      vec3    // unit vector: where it sits inside a cluster
	rate     float64 // how quickly it follows its target, 1/s

	pos vec3    // in the ring's own frame: the ring lies in x/y round the z axis
	b   float64 // brightness
}

// scene eases every particle toward the layout of the state shown, and the
// ring's orientation, colour and motion toward that state's, so every change
// flows: particles stream from ring to clusters to one point and back.
type scene struct {
	ready    bool
	ps       [nParticles]particle
	kind     Kind
	age      float64 // seconds in kind
	t        float64
	presence float64 // 0 hidden .. 1 fully shown
	level    float64 // smoothed microphone level

	phi, omega float64 // spin round the ring's axis and its speed
	head       float64 // the transcribing comet's angle in the ring's frame
	tilt, roll float64 // the ring's tilt away from facing us, and its turn on screen
	yaw        float64 // the problem's head shake
	wave       float64 // how much the ring ripples with the voice
	turb       float64 // how far particles wander from their places
	comet      float64 // 0..1: brightness gathers into the comet
	gain       float64
	flash      float64
	col        rgb

	light [Canvas * Canvas]rgb
	glow  [half * half]rgb
	small [half * half]rgb
	large [half * half]rgb
	tmp   [half * half]rgb
}

func (sc *scene) setup() {
	r := rand.New(rand.NewPCG(7, 1977))
	// Particles bunch into a few clumps along the ring, so it reads as turning.
	var clumps [16]float64
	for i := range clumps {
		clumps[i] = r.Float64() * 2 * math.Pi
	}
	for i := range sc.ps {
		p := &sc.ps[i]
		if r.Float64() < 0.55 {
			p.u = clumps[r.IntN(len(clumps))] + r.NormFloat64()*0.13
		} else {
			p.u = r.Float64() * 2 * math.Pi
		}
		p.v = r.Float64() * 2 * math.Pi
		p.rr = math.Sqrt(r.Float64())
		p.bright = 0.55 + 0.45*r.Float64()
		p.size = 0.6 + 0.5*math.Pow(r.Float64(), 2)
		if r.Float64() < 0.07 {
			p.size, p.bright = 1.15+0.25*r.Float64(), 1 // a few big sparkles
		}
		if r.Float64() < 0.09 && i%glyphEvery != 0 {
			// A stray, drifting further out like dust round the ring.
			p.rr = 1.4 + 2.2*r.Float64()
			p.bright = 0.25 + 0.35*r.Float64()
			p.size = 0.62 + 0.15*r.Float64()
		}
		p.drift = -0.08 + 0.26*r.Float64()
		p.tw = 1.2 + 2.2*r.Float64()
		p.ph = r.Float64() * 2 * math.Pi
		for j := range 3 {
			p.w[j] = 0.5 + 1.4*r.Float64()
			p.p[j] = r.Float64() * 2 * math.Pi
		}
		z := 2*r.Float64() - 1
		a := r.Float64() * 2 * math.Pi
		q := math.Sqrt(1 - z*z)
		c := math.Cbrt(r.Float64())
		p.dir = vec3{c * q * math.Cos(a), c * q * math.Sin(a), c * z}
		p.rate = 3.6 + 4.2*r.Float64()
	}
	sc.tilt, sc.roll, sc.col, sc.gain = 0.84, -0.42, icy, 1
	sc.ready = true
}

// torus is a place on a ring of radius R with a tube of radius tube.
func torus(a, v, off, R float64) vec3 {
	rad := R + off*math.Cos(v)
	return vec3{rad * math.Cos(a), rad * math.Sin(a), off * math.Sin(v)}
}

// target is where particle i should be, and how bright, for kind.
func (sc *scene) target(i int, kind Kind, s State, t float64) (vec3, float64) {
	p := &sc.ps[i]
	isGlyph := i%glyphEvery == 0
	switch kind {
	case Transcribing:
		return torus(p.u+p.drift*t*1.6, p.v, 1.7*p.rr, 27), p.bright
	case Queued:
		n := min(max(s.Queued, 1), maxQueued)
		if i%10 < 7 {
			c := (i/10*7 + i%10) % n
			ca := 2 * math.Pi * float64(c) / float64(n)
			pulse := 1 + 0.14*math.Sin(t*2.3+float64(c)*2.1)
			br := (3.0 + 2.2/float64(n)) * pulse
			// Each cluster swirls slowly round itself, like a little galaxy.
			sw := t * (0.9 + 0.2*float64(c%3))
			dx := p.dir[0]*math.Cos(sw) - p.dir[2]*math.Sin(sw)
			dz := p.dir[0]*math.Sin(sw) + p.dir[2]*math.Cos(sw)
			cx, cy := 28*math.Cos(ca), 28*math.Sin(ca)
			g := math.Min(1.15, 0.5+0.22*float64(n))
			return vec3{cx + br*dx, cy + br*p.dir[1], br * dz}, p.bright * g
		}
		return torus(p.u+p.drift*t, p.v, 3.2*p.rr, 28), p.bright * 0.3
	case Done:
		if sc.age < merge {
			return vec3{4.5 * p.dir[0], 4.5 * p.dir[1], 4.5 * p.dir[2]}, p.bright * 1.25
		}
		if isGlyph {
			return sc.unspin(glyphPoint(checkMark, i/glyphEvery, p)), p.bright * 1.1
		}
		return torus(p.u+p.drift*t*0.5, p.v, 1.1*min(p.rr, 2.5), 31), p.bright * 0.95
	case Problem:
		if isGlyph {
			return sc.unspin(glyphPoint(bang, i/glyphEvery, p)), p.bright * 1.1
		}
		return torus(p.u+p.drift*t, p.v, 2.0*p.rr, 29), p.bright * 0.9
	}
	// Recording.
	return torus(p.u+p.drift*t, p.v, (2.3+3.2*sc.level)*p.rr, 33), p.bright
}

// unspin turns a point given as seen on screen into the ring's spinning frame,
// so a glyph drawn by the particles stands still while the ring turns.
func (sc *scene) unspin(q vec3) vec3 {
	c, s := math.Cos(sc.phi), math.Sin(sc.phi)
	return vec3{q[0]*c + q[1]*s, -q[0]*s + q[1]*c, q[2]}
}

type glyph int

const (
	checkMark glyph = iota
	bang
)

var checkPath = [][2]float64{{-12, -0.5}, {-4.5, 7.5}, {12.5, -9.5}}

// glyphPoint is where the g-th of nGlyph particles sits on a glyph, screen
// aligned (x right, y down), with a little depth so it looks made of dust.
func glyphPoint(kind glyph, g int, p *particle) vec3 {
	f := (float64(g) + 0.5) / nGlyph
	jit := 0.55 * p.rr
	jx, jy, jz := jit*math.Cos(p.v), jit*math.Sin(p.v), 1.2*p.dir[2]
	switch kind {
	case bang:
		if f < 0.76 {
			return vec3{jx, -14 + 18*f/0.76 + jy, jz}
		}
		return vec3{1.8 * p.dir[0], 10 + 1.8*p.dir[1], jz}
	}
	// The check, by length along its two strokes.
	var total float64
	for i := 1; i < len(checkPath); i++ {
		total += math.Hypot(checkPath[i][0]-checkPath[i-1][0], checkPath[i][1]-checkPath[i-1][1])
	}
	d := f * total
	for i := 1; i < len(checkPath); i++ {
		a, b := checkPath[i-1], checkPath[i]
		l := math.Hypot(b[0]-a[0], b[1]-a[1])
		if d <= l || i == len(checkPath)-1 {
			k := math.Min(d/l, 1)
			return vec3{a[0] + (b[0]-a[0])*k + jx, a[1] + (b[1]-a[1])*k + jy, jz}
		}
		d -= l
	}
	return vec3{}
}

// step moves the ring a frame (dt seconds) toward kind's look and reports
// whether anything is still visible.
func (sc *scene) step(kind Kind, s State, t, dt float64) bool {
	if !sc.ready {
		sc.setup()
	}
	if kind != sc.kind {
		sc.kind, sc.age = kind, 0
	} else {
		sc.age += dt
	}
	sc.t = t
	lt := 0.0
	if kind == Recording {
		lt = math.Max(0, math.Min(1, s.Level))
	}
	sc.level += (lt - sc.level) * min(1, dt*14)

	spawn := false
	if kind == Hidden {
		sc.presence = math.Max(0, sc.presence-dt/0.75)
		if sc.presence == 0 {
			return false
		}
	} else {
		spawn = sc.presence == 0
		sc.presence = math.Min(1, sc.presence+dt/0.5)
	}

	lv := sc.level
	tilt, roll, omega, col := sc.tilt, sc.roll, sc.omega, sc.col
	wave, turb, comet, gain := 0.0, 0.3, 0.0, 1.0
	yaw := 0.0
	switch kind {
	case Recording:
		tilt, roll = 0.84+0.08*math.Sin(t*0.23), -0.42+0.2*math.Sin(t*0.29)
		omega, wave, turb, gain, col = 0.4+1.5*lv, lv, 0.35+1.0*lv, 0.72+0.8*lv, icy
	case Transcribing:
		tilt, roll = 0.92+0.14*math.Sin(t*1.3), -0.3+0.32*math.Sin(t*0.8)
		omega, comet, turb, gain, col = 1.3, 1, 0.25, 1.05, lavender
	case Queued:
		tilt, roll = 0.62, -0.25+0.1*math.Sin(t*0.4)
		omega, turb, col = 0.5, 0.25, azure
	case Done:
		tilt, roll, omega, turb, gain, col = 0.5, 0, 3.4, 0.2, 1.1, mint
		if sc.age >= merge {
			tilt, omega = 0, 0.12
		}
	case Problem:
		tilt, roll, omega, gain, col = 0.16, 0, 0, 1, amber
		turb = 0.4 + 1.8*math.Exp(-sc.age*1.6)
		yaw = 0.42 * math.Sin(sc.age*16) * math.Exp(-sc.age*2.6)
	case Hidden:
		turb, gain = 1.6, sc.gain
	}
	ease := func(cur *float64, to, rate float64) { *cur += (to - *cur) * (1 - math.Exp(-dt*rate)) }
	if spawn {
		sc.tilt, sc.roll, sc.col, sc.omega = tilt, roll, col, omega
	}
	ease(&sc.tilt, tilt, 3.5)
	ease(&sc.roll, roll, 3.5)
	ease(&sc.omega, omega, 2.5)
	for j := range sc.col {
		ease(&sc.col[j], col[j], 5)
	}
	ease(&sc.wave, wave, 10)
	ease(&sc.turb, turb, 5)
	ease(&sc.comet, comet, 4)
	ease(&sc.gain, gain, 6)
	if kind == Problem {
		sc.yaw = yaw
	} else {
		ease(&sc.yaw, 0, 8)
	}
	if kind == Done && sc.age >= merge {
		a := sc.age - merge
		sc.flash = (1 - math.Exp(-a*30)) * math.Exp(-a*3)
	} else {
		ease(&sc.flash, 0, 6)
	}
	sc.phi += sc.omega * dt
	sc.head += dt * 3.3

	rateMul := 1.0
	if kind == Done && sc.age < merge {
		rateMul = 1.25 // merging is brisk
	}
	for i := range sc.ps {
		p := &sc.ps[i]
		if kind == Hidden {
			// Dissolve: drift outward and let the light go.
			for j := range p.pos {
				p.pos[j] *= 1 + 0.42*dt
			}
			continue
		}
		to, b := sc.target(i, kind, s, t)
		if spawn {
			p.pos = vec3{to[0] * 0.15, to[1] * 0.15, to[2] * 0.15}
			p.b = b
		}
		k := 1 - math.Exp(-dt*p.rate*rateMul)
		for j := range p.pos {
			p.pos[j] += (to[j] - p.pos[j]) * k
		}
		p.b += (b - p.b) * (1 - math.Exp(-dt*6))
	}
	return true
}
