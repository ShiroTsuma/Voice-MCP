// Package liquid shows dictation's state in the bottom right corner of the
// screen as a small gooey core of liquid light inside a ring of orbiting
// sparks, on a soft dark halo. The voice sheds sparks out of the core and the
// ring swells and ripples with it while recording; a bright comet sweeps the
// tightened ring while transcribing; every message waiting for a busy agent is
// a droplet with its own swarm of sparks; on delivery everything flows into the
// core, which flashes and bursts; a failure shakes the core amber and lets the
// sparks fall. It never takes focus and lets clicks through.
package liquid

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
	nSparks  = 110
	maxQueue = 7
	queueR   = 31 // where the waiting droplets orbit
)

type rgb [3]float64

var (
	ice      = rgb{0.42, 0.86, 1.00}
	lavender = rgb{0.62, 0.50, 1.00}
	azure    = rgb{0.36, 0.62, 1.00}
	mint     = rgb{0.36, 1.00, 0.70}
	amber    = rgb{1.00, 0.60, 0.16}
)

// appearance is the part of a state's appearance that eases from state to state.
type appearance struct {
	ringR, spread float64 // ring radius and thickness, px
	spin          float64 // ring rotation, rad/s
	bright        float64 // spark brightness
	coreR         float64 // core radius, px
	orbit, churn  float64 // how far and how fast the core's lumps swirl
	comet         float64 // 1: a bright head sweeps the ring
	tint          rgb
}

func (a *appearance) ease(b appearance, k float64) {
	e := func(v *float64, to float64) { *v += (to - *v) * k }
	e(&a.ringR, b.ringR)
	e(&a.spread, b.spread)
	e(&a.spin, b.spin)
	e(&a.bright, b.bright)
	e(&a.coreR, b.coreR)
	e(&a.orbit, b.orbit)
	e(&a.churn, b.churn)
	e(&a.comet, b.comet)
	for j := range a.tint {
		e(&a.tint[j], b.tint[j])
	}
}

type spark struct {
	th, r, vis float64 // polar position from the centre, visibility 0..1
	dy, vy     float64 // falling away after a problem
	alive      bool    // false: on its way into the core
	recycle    bool    // dives into the core and comes out again
	burst      bool    // thrown out by a delivery
	// Fixed at birth.
	rank                 int // the order sparks join the ring in
	off, spd, rate, size float64
	tw, ph, u, v         float64
}

type droplet struct{ ang, dist, rad float64 }

// scene eases everything toward each state's look, so every change of state
// flows: sparks are drawn into the core and shed from it, droplets merge.
type scene struct {
	ready bool
	rng   uint32
	sp    [nSparks]spark
	q     [maxQueue]droplet
	kind  Kind
	since float64 // seconds in kind
	t     float64
	level float64 // smoothed microphone level
	pres  float64 // 0 hidden .. 1 shown
	lk    appearance
	spin  float64 // the ring's turn so far
	churn float64 // the core lumps' turn so far
	qBase float64 // the droplets' turn so far
	kick  float64 // the core swelling after swallowing sparks
	flash float64 // the delivery's flash, 1 fading to 0
	shake float64 // the core's sideways offset, px
	// The glyph in the core: a check after a delivery, "!" on a problem.
	markKind     Kind
	markA, markP float64 // its opacity, how far it is drawn
	em           [Canvas * Canvas * 3]float32
}

func (sc *scene) rand() float64 {
	x := sc.rng
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	sc.rng = x
	return float64(x>>8) / (1 << 24)
}

func (sc *scene) init() {
	sc.ready, sc.rng = true, 0x9e3779b9
	perm := make([]int, nSparks)
	for i := range perm {
		perm[i] = i
	}
	for i := nSparks - 1; i > 0; i-- {
		j := int(sc.rand() * float64(i+1))
		perm[i], perm[j] = perm[j], perm[i]
	}
	for i := range sc.sp {
		p := &sc.sp[i]
		p.rank = perm[i]
		p.off = (sc.rand() + sc.rand() + sc.rand() - 1.5) / 1.5
		p.spd = 0.7 + 0.6*sc.rand()
		p.rate = 2.4 + 3.6*sc.rand()
		p.size = math.Pow(sc.rand(), 2.5)
		p.tw = 1.5 + 5*sc.rand()
		p.ph = 2 * math.Pi * sc.rand()
		p.u, p.v = sc.rand(), sc.rand()
		p.th = 2 * math.Pi * sc.rand()
	}
	sc.lk = looks(Recording, 0)
	sc.lk.coreR = 0
}

// looks is the look each state eases toward; l is the microphone level.
func looks(k Kind, l float64) appearance {
	switch k {
	case Transcribing:
		return appearance{ringR: 24, spread: 1.6, spin: 1.5, bright: 0.95, coreR: 9.5, orbit: 0.42, churn: 5.5, comet: 1, tint: lavender}
	case Queued:
		return appearance{ringR: queueR, spread: 0, spin: 0.5, bright: 0.85, coreR: 8.5, orbit: 0.2, churn: 1.2, tint: azure}
	case Done:
		return appearance{ringR: 28, spread: 1, spin: 3, bright: 1, coreR: 12.5, orbit: 0.12, churn: 2, tint: mint}
	case Problem:
		return appearance{ringR: 30, spread: 4, spin: 0.25, bright: 0.85, coreR: 12, orbit: 0.2, churn: 1.5, tint: amber}
	}
	return appearance{ringR: 30 + 10*l, spread: 2.5 + 4.5*l, spin: 0.6 + 0.9*l, bright: 0.62 + 0.38*l,
		coreR: 9.5 + 3.5*l, orbit: 0.38 + 0.4*l, churn: 1.4 + 2.5*l, tint: ice}
}

// coreSize is the core's radius as drawn now.
func (sc *scene) coreSize() float64 {
	p := smooth(0, 1, sc.pres)
	return sc.lk.coreR * p * (1 + 0.07*math.Min(sc.kick, 2.5))
}

func wrap(a float64) float64 {
	a = math.Mod(a+math.Pi, 2*math.Pi)
	if a < 0 {
		a += 2 * math.Pi
	}
	return a - math.Pi
}

func ease(dt, rate float64) float64 { return 1 - math.Exp(-dt*rate) }

// step moves everything a frame (dt seconds) toward kind's look and reports
// whether anything is still visible.
func (sc *scene) step(kind Kind, s State, t, dt float64) bool {
	if !sc.ready {
		sc.init()
	}
	if kind != sc.kind {
		sc.kind, sc.since = kind, 0
	} else {
		sc.since += dt
	}
	sc.t = t
	// The level rises fast and settles slowly, like a VU meter.
	lvl := clamp(s.Level)
	if kind != Recording {
		lvl = 0
	}
	if lvl > sc.level {
		sc.level += (lvl - sc.level) * ease(dt, 16)
	} else {
		sc.level += (lvl - sc.level) * ease(dt, 5)
	}
	if kind != Hidden {
		sc.lk.ease(looks(kind, sc.level), ease(dt, 5))
		sc.pres = math.Min(1, sc.pres+dt/0.55)
	} else {
		sc.pres = math.Max(0, sc.pres-dt/0.75)
	}
	sc.spin += sc.lk.spin * dt
	sc.churn += sc.lk.churn * dt
	sc.qBase += 0.55 * dt
	sc.kick *= math.Exp(-dt * 4)
	if kind == Done && sc.since >= 0.3 && sc.since < 0.38 {
		// Everything is in: the core lights up white in a few frames.
		sc.flash = math.Min(1, sc.flash+dt/0.07)
	} else {
		sc.flash *= math.Exp(-dt * 3.2)
	}
	if kind == Problem {
		sc.shake = 3.4 * math.Sin(sc.since*31) * math.Exp(-sc.since*2.6)
	} else {
		sc.shake *= math.Exp(-dt * 10)
	}

	switch {
	case kind == Done && sc.since > 0.5, kind == Problem && sc.since > 0.12:
		if sc.markKind != kind {
			sc.markKind, sc.markA, sc.markP = kind, 0, 0
		}
		sc.markA = math.Min(1, sc.markA+dt/0.12)
		sc.markP = math.Min(1, sc.markP+dt/0.32)
	default:
		sc.markA = math.Max(0, sc.markA-dt/0.25)
		if sc.markA == 0 {
			sc.markKind = Hidden // drawn afresh next time
		}
	}

	core := sc.coreSize()
	sc.stepDroplets(kind, s, dt, core)
	nq := 0
	if kind == Queued {
		nq = min(max(s.Queued, 0), maxQueue)
	}
	sc.stepSparks(kind, dt, core, nq)

	if sc.pres > 0 {
		return true
	}
	for i := range sc.sp {
		if sc.sp[i].vis > 0 {
			return true
		}
	}
	return false
}

func (sc *scene) stepDroplets(kind Kind, s State, dt, core float64) {
	n := 0
	if kind == Queued {
		n = min(max(s.Queued, 0), maxQueue)
	}
	for k := range sc.q {
		d := &sc.q[k]
		if k < n {
			ang := sc.qBase + 2*math.Pi*float64(k)/float64(n)
			if d.rad < 0.3 {
				// A new one pinches off the core.
				d.dist, d.ang = 0, ang
			}
			d.ang += wrap(ang-d.ang) * ease(dt, 6)
			d.dist += (queueR - d.dist) * ease(dt, 4)
			d.rad += (5 - d.rad) * ease(dt, 6)
			continue
		}
		// Gone: it flows back into the core and melts there.
		d.ang += 0.55 * dt
		rate := 4.0
		if kind == Done {
			rate = 7
		}
		d.dist += (0 - d.dist) * ease(dt, rate)
		if d.dist < core*0.8 || core < 1 {
			d.rad += (0 - d.rad) * ease(dt, 8)
			if d.rad < 0.05 {
				d.rad = 0
			}
		}
	}
}

// wants reports whether spark p should be out of the core in kind.
func (sc *scene) wants(kind Kind, p *spark, queued int) bool {
	if !p.alive && sc.pres < 0.4 {
		return false // the core grows first, then sheds its sparks
	}
	switch kind {
	case Recording:
		// The voice sheds sparks into the ring; silence takes them back.
		return float64(p.rank) < (0.42+0.58*sc.level)*nSparks
	case Transcribing:
		return p.rank < nSparks*3/4
	case Queued:
		return p.rank < min(nSparks, 10*queued)
	case Done:
		return sc.since >= 0.34 && sc.since < 0.6
	case Problem:
		return p.alive
	}
	return false
}

func (sc *scene) stepSparks(kind Kind, dt, core float64, nq int) {
	lk := &sc.lk
	l := sc.level
	// Recording recycles a few sparks: they dive into the core and pop out
	// elsewhere, more of them the louder the voice.
	if kind == Recording && sc.rand() < (0.8+5*l)*dt*3 {
		p := &sc.sp[int(sc.rand()*nSparks)%nSparks]
		if p.alive && !p.recycle && p.r > core*1.6 {
			p.recycle = true
		}
	}
	for i := range sc.sp {
		p := &sc.sp[i]
		want := sc.wants(kind, p, nq) && !p.recycle
		if want && !p.alive {
			p.alive = true
			p.burst = kind == Done
			if p.vis < 0.05 || p.r < core {
				// Shed: it comes out of the core's edge.
				p.r, p.vis, p.dy, p.vy = core*0.55, 1, 0, 0
				if !p.burst {
					p.th = sc.spin + 2*math.Pi*sc.rand()
				}
			}
		}
		if !want && !p.burst { // thrown sparks fly on until they fade
			p.alive = false
		}
		tr, rate := 0.0, p.rate
		aim, hasAim := 0.0, false
		switch {
		case !p.alive:
			// Into the core.
			rate *= 1.5
			if kind == Done || kind == Hidden {
				rate = 6 + 4*p.u
			}
		case p.burst:
			tr, rate = 66, 5+4*p.u
		case kind == Queued && nq > 0:
			d := sc.q[p.rank%nq]
			dir := 1.0
			if p.rank%2 == 1 {
				dir = -1
			}
			la := p.ph + sc.t*(1.4+1.6*p.u)*dir
			lr := 5.5 + 4*p.v
			x := d.dist*math.Cos(d.ang) + lr*math.Cos(la)
			y := d.dist*math.Sin(d.ang) + lr*0.85*math.Sin(la)
			tr, aim, hasAim = math.Hypot(x, y), math.Atan2(y, x), true
			rate = 4 + 2*p.u
		default:
			tr = lk.ringR + lk.spread*p.off
			if kind == Recording {
				// The ring ripples like a waveform with the voice.
				tr += l * (3.6*math.Sin(3*p.th+2.3*sc.t) + 2.6*math.Sin(5*p.th-3.1*sc.t+1.3))
			}
			if kind == Problem {
				tr += 1.6 * math.Sin(sc.t*13+p.ph*7)
			}
		}
		if hasAim {
			p.th += wrap(aim-p.th) * ease(dt, 7)
		} else {
			// Faster closer in, like a whirlpool.
			w := lk.spin * p.spd * math.Min(2.6, math.Max(1, lk.ringR/math.Max(p.r, 4)))
			p.th += w * dt
		}
		p.r += (tr - p.r) * ease(dt, rate)
		if p.r < 0 {
			p.r = 0
		}
		// Problem: half the ring loses its orbit and falls.
		if kind == Problem && p.alive && p.u < 0.7 && sc.since > 0.12+0.6*p.v {
			p.vy += (70 + 90*p.u) * dt
			p.dy += p.vy * dt
		} else if kind != Problem {
			p.dy += (0 - p.dy) * ease(dt, 6)
			p.vy = 0
		}
		if p.dy > 34 {
			p.vis = math.Max(0, p.vis-dt*3)
		}
		if p.burst && p.r > 54 {
			p.vis, p.burst = 0, false
		}
		if !p.alive && p.vis > 0 && p.r < math.Max(core*0.6, 1.5) {
			// Swallowed: the core gulps.
			p.vis = 0
			sc.kick += 0.09
			p.recycle = false
		}
		if !p.alive && p.vis == 0 {
			p.recycle = false
		}
		if sc.pres == 0 && kind == Hidden {
			p.vis = math.Max(0, p.vis-dt*4)
		}
	}
}

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func smooth(e0, e1, v float64) float64 {
	t := clamp((v - e0) / (e1 - e0))
	return t * t * (3 - 2*t)
}
