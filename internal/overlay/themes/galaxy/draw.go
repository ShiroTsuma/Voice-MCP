// Package galaxy shows dictation's state in the bottom right corner of the
// screen as a tiny galaxy of glowing particles on a soft dark halo: its arms
// swell and ripple with the voice while recording, it turns into a vortex
// pouring into a bright core while transcribing, settles into a dim
// accretion ring carrying one bright star per message waiting for a busy
// agent, swallows those stars in a mint flare when a message arrives and
// flickers amber when something fails. It never takes focus and lets clicks
// through.
package galaxy

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
	center   = Canvas / 2.0
	diskR    = 50.0 // radius of the disk at spread 1, in pixels
	starR    = 25.0 // radius of the queued stars' orbit, in the dark inside the ring
	nBulge   = 120
	nArm     = 470
	nDust    = 190
	nPart    = nBulge + nArm + nDust
	maxStars = 7
)

type rgb [3]float64

func mix(a, b, t float64) float64 { return a + (b-a)*t }

func mixc(a, b rgb, t float64) rgb {
	return rgb{mix(a[0], b[0], t), mix(a[1], b[1], t), mix(a[2], b[2], t)}
}

// Light colours; they are added up and tone mapped, so they may exceed 1.
var (
	white  = rgb{1, 1, 1}
	ice    = rgb{0.36, 0.80, 1.00}
	frost  = rgb{0.30, 0.58, 1.00}
	violet = rgb{0.66, 0.50, 1.00}
	orchid = rgb{0.78, 0.48, 1.00}
	steel  = rgb{0.46, 0.68, 1.00}
	mint   = rgb{0.42, 1.00, 0.72}
	aqua   = rgb{0.50, 1.00, 0.95}
	amber  = rgb{1.00, 0.52, 0.14}
	ember  = rgb{1.00, 0.26, 0.12}
	halo   = rgb{0.020, 0.035, 0.065} // the dark backdrop
)

type particle struct {
	u, home, ring float64 // radius as a fraction of the disk: now, at rest, in the ring
	arm, ringAng  float64 // angle of its arm, angle in the ring
	jit, focus    float64 // off its arm; how much closer to it in the vortex
	dust          bool
	spin          float64 // its own turning, from the vortex
	bright, sig   float64 // brightness, sprite size (px)
	tint, white   float64 // toward the accent colour, toward white
	tw, twf       float64 // twinkle phase and rate
	fade          float64 // 0 right after re-entering the vortex at its rim
	// Where it is drawn this frame, from where it was last frame (motion blur).
	x0, y0, x1, y1, b float64
	c                 rgb
	fresh             bool
}

type star struct{ r, ang, a float64 }

// shape is the galaxy's shape and colour; the scene eases it toward each
// state's shape, so every change of state flows.
type shape struct {
	spread  float64 // disk radius scale
	spin    float64 // pattern rotation, rad/s
	inflow  float64 // vortex: how fast particles fall in
	swirl   float64 // vortex: how much faster the inside turns
	tight   float64 // spiral winding
	ringW   float64 // 0 spiral .. 1 accretion ring
	bright  float64 // particle brightness
	core    float64 // core brightness
	coreW   float64 // core size
	chaos   float64 // turbulence
	ripple  float64 // voice ripples
	beam    float64 // one side of the ring brighter, as in an accretion disk
	sparkle float64 // twinkle amount
	warn    float64 // the amber warning ring
	colA    rgb     // main colour
	colB    rgb     // accent colour
	coreC   rgb
}

// scene is the galaxy between frames.
type scene struct {
	ready   bool
	ps      [nPart]particle
	stars   [maxStars]star
	cur     shape
	present float64 // 0 hidden .. 1 fully there, moves linearly
	level   float64 // smoothed microphone level
	kind    Kind
	since   float64 // when kind started
	phase   float64 // pattern angle
	qphase  float64 // queued stars' angle
	burst   float64 // when the Done flare started, or -1
	shake   float64 // horizontal offset, px
	t       float64
	merged  float64 // stars swallowed in this Done, brightens the flare

	acc, b1, b2, tmp []float64
}

func (sc *scene) init() {
	r := rand.New(rand.NewPCG(4, 2026))
	for i := range sc.ps {
		p := &sc.ps[i]
		switch {
		case i < nBulge: // the bright core
			p.home = 0.015 + math.Abs(r.NormFloat64())*0.11
			// Round at rest, but on an arm once the vortex pulls it in.
			p.arm = float64(i%2) * math.Pi
			p.jit = (r.Float64() - 0.5) * 2 * math.Pi
			p.focus = 0.8
			p.ring = 0.76 + r.NormFloat64()*0.05
			p.bright = 0.9 + 0.9*r.Float64()
			p.sig = 0.55 + 0.25*r.Float64()
			p.white = 0.55
		case i < nBulge+nArm: // two arms
			p.home = 0.12 + 0.9*math.Pow(r.Float64(), 0.85)
			p.arm = float64(i%2) * math.Pi
			p.jit = r.NormFloat64() * (0.18 + 0.22*p.home)
			p.focus = 0.45
			p.ring = 0.77 + r.NormFloat64()*0.055
			p.bright = 0.45 + 1.9*math.Pow(r.Float64(), 3)
			p.sig = 0.55 + 0.2*r.Float64() + 0.25*(p.bright-0.45)/1.9
			p.tint = math.Pow(r.Float64(), 2.5) * 0.8
			p.white = 0.25 * r.Float64()
		default: // faint dust between and around the arms
			p.home = 0.18 + 1.0*r.Float64()
			p.arm = r.Float64() * 2 * math.Pi
			p.ring = 0.80 + r.NormFloat64()*0.13
			p.bright = 0.14 + 0.3*r.Float64()
			p.sig = 0.5 + 0.15*r.Float64()
			p.tint = 0.4 + 0.6*r.Float64()
			p.dust = true
		}
		p.u = p.home
		p.ringAng = r.Float64() * 2 * math.Pi
		p.tw = r.Float64() * 2 * math.Pi
		p.twf = 1.5 + 3.5*r.Float64()
		p.fade = 1
		p.fresh = true
	}
	sc.cur = sc.target(Recording, State{}, 0)
	sc.burst = -1
	sc.acc = make([]float64, Canvas*Canvas*3)
	sc.b1 = make([]float64, (Canvas/2)*(Canvas/2)*3)
	sc.b2 = make([]float64, (Canvas/4)*(Canvas/4)*3)
	sc.tmp = make([]float64, (Canvas/2)*(Canvas/2)*3)
	sc.ready = true
}

// target is kind's shape at time t.
func (sc *scene) target(kind Kind, s State, t float64) shape {
	l := sc.level
	g := shape{spread: 0.78, spin: 0.3, tight: 2.4, bright: 0.8, core: 1, coreW: 1,
		sparkle: 0.25, colA: ice, colB: violet, coreC: rgb{0.85, 0.95, 1}}
	tau := t - sc.since
	switch kind {
	case Hidden:
		g = sc.cur // collapse with the shape it has
		g.spin = sc.cur.spin + 1.2
	case Recording:
		g.spread = 0.68 + 0.4*l
		g.spin = 0.28 + 0.9*l
		g.bright = 0.45 + 1.05*l
		g.core = 0.75 + 1.3*l
		g.coreW = 0.9 + 0.35*l
		g.ripple = l
		g.sparkle = 0.25 + 0.5*l
	case Transcribing:
		g.spread = 0.9
		g.spin = 1.5
		g.inflow = 0.6
		g.swirl = 2.4
		g.tight = 3.4
		g.bright = 0.85
		g.core = 1.7 + 0.25*math.Sin(t*5)
		g.coreW = 1.1
		g.colA, g.colB = frost, orchid
		g.coreC = rgb{0.80, 0.85, 1}
	case Queued:
		g.spread = 0.95
		g.spin = 0.2
		g.tight = 0.6
		g.ringW = 0.92
		g.bright = 0.3
		g.core = 0.38 + 0.06*math.Sin(t*2)
		g.coreW = 0.85
		g.beam = 0.7
		g.sparkle = 0.35
		g.colA, g.colB = steel, violet
	case Done:
		if sc.burst < 0 {
			// Drawing in while the stars fall into the core.
			g = sc.cur
			g.spread = math.Min(sc.cur.spread, 0.8)
			g.spin = 0.7
			g.core = 1.3
			g.bright = math.Max(sc.cur.bright, 0.45)
			break
		}
		// Reborn from the flare as a mint galaxy.
		g.colA, g.colB = mint, aqua
		g.coreC = rgb{0.80, 1, 0.92}
		g.spread, g.bright, g.core = 0.84, 0.8, 1.0
		g.spin = 0.6
		g.tight = 2.2
		g.sparkle = 0.5
	case Problem:
		g.colA, g.colB = amber, ember
		g.coreC = rgb{1, 0.62, 0.35}
		g.spread = 0.8
		g.spin = -0.25
		g.tight = 1.8
		g.chaos = 1.2
		g.bright = 0.8
		pulse := 0.5 + 0.5*math.Cos(tau*2*math.Pi*1.9)
		g.core = 0.8 + 1.1*pulse
		g.coreW = 1.1
		g.sparkle = 0.6
		g.warn = 0.1 + 0.9*pulse
	}
	return g
}

// step moves the galaxy a frame (dt seconds) toward kind and reports whether
// anything is still visible.
func (sc *scene) step(kind Kind, s State, t, dt float64) bool {
	if !sc.ready {
		sc.init()
		sc.since = t
		sc.kind = kind
	}
	if kind != sc.kind {
		sc.kind, sc.since = kind, t
		if kind == Done {
			sc.burst, sc.merged = -1, 0
		}
	}
	sc.t = t
	tau := t - sc.since
	// The level jumps up quickly and sinks slowly, like a VU meter.
	lv := s.Level
	if kind != Recording {
		lv = 0
	}
	if lv > sc.level {
		sc.level += (lv - sc.level) * (1 - math.Exp(-dt*20))
	} else {
		sc.level += (lv - sc.level) * (1 - math.Exp(-dt*7))
	}
	// Appearing and vanishing take about half a second.
	if kind == Hidden {
		sc.present = math.Max(0, sc.present-dt/0.55)
	} else {
		sc.present = math.Min(1, sc.present+dt/0.5)
	}
	if kind == Done && sc.burst < 0 {
		delay := 0.12
		for _, st := range sc.stars {
			if st.a > 0.05 {
				delay = 0.4
			}
		}
		if tau >= delay {
			sc.burst = t
		}
	}

	g := sc.target(kind, s, t)
	k := 1 - math.Exp(-dt*5)
	kf := 1 - math.Exp(-dt*14) // voice-driven parts follow quickly
	c := &sc.cur
	ease := func(v *float64, to, k float64) { *v += (to - *v) * k }
	if kind == Recording {
		ease(&c.spread, g.spread, kf)
	} else {
		ease(&c.spread, g.spread, k)
	}
	ease(&c.spin, g.spin, k)
	ease(&c.inflow, g.inflow, k)
	ease(&c.swirl, g.swirl, k)
	ease(&c.tight, g.tight, k)
	ease(&c.ringW, g.ringW, k)
	ease(&c.bright, g.bright, kf)
	ease(&c.core, g.core, kf)
	ease(&c.coreW, g.coreW, k)
	ease(&c.chaos, g.chaos, k)
	ease(&c.ripple, g.ripple, kf)
	ease(&c.beam, g.beam, k)
	ease(&c.sparkle, g.sparkle, k)
	ease(&c.warn, g.warn, kf)
	for j := 0; j < 3; j++ {
		ease(&c.colA[j], g.colA[j], k)
		ease(&c.colB[j], g.colB[j], k)
		ease(&c.coreC[j], g.coreC[j], k)
	}
	if kind == Problem {
		sc.shake = 3.2 * math.Sin(tau*2*math.Pi*4.2) * math.Exp(-tau*2.6)
	} else {
		sc.shake *= 1 - k
	}

	pv := smooth(0, 1, sc.present)
	sc.phase = math.Mod(sc.phase+dt*c.spin+dt*(1-pv)*3, 2*math.Pi)
	sc.qphase = math.Mod(sc.qphase+dt*0.45, 2*math.Pi)
	sc.stepStars(kind, s, dt)
	sc.stepParticles(dt, pv)
	return sc.present > 0 || sc.burstLight() > 0.002 || sc.starsVisible()
}

func (sc *scene) starsVisible() bool {
	for _, st := range sc.stars {
		if st.a > 0.01 {
			return true
		}
	}
	return false
}

func (sc *scene) stepStars(kind Kind, s State, dt float64) {
	n := 0
	if kind == Queued {
		n = min(max(s.Queued, 0), maxStars)
	}
	k := 1 - math.Exp(-dt*5)
	for i := range sc.stars {
		st := &sc.stars[i]
		switch {
		case i < n:
			to := sc.qphase + 2*math.Pi*float64(i)/float64(n)
			if st.a < 0.02 {
				st.r, st.ang = 0, to // a new star rises out of the core
			}
			st.r += (starR - st.r) * k
			st.ang += angDiff(to, st.ang) * k
			st.a += (1 - st.a) * k
		case kind == Done:
			// Falling into the core, turning faster the deeper it gets.
			f := st.r / starR
			st.r = math.Max(0, st.r-dt*(45+150*(1-f)))
			st.ang += dt * 3 / (0.25 + f)
			if st.r < 6 && st.a > 0 {
				drop := math.Min(st.a, dt*6)
				st.a -= drop
				sc.merged += drop
			}
		default:
			st.a = math.Max(0, st.a-dt*3)
			st.r *= 1 - k*0.6
		}
	}
}

func (sc *scene) stepParticles(dt, pv float64) {
	c := &sc.cur
	t := sc.t
	inflN := clamp(c.inflow / 0.6)
	relax := 1 - math.Exp(-dt*1.6*(1-inflN))
	R := diskR * c.spread * (0.12 + 0.88*pv)
	cx := center + sc.shake
	burstPush := 0.0
	if sc.burst >= 0 {
		a := t - sc.burst
		burstPush = 0.15 * math.Sin(math.Min(a/0.5, 1)*math.Pi) * math.Exp(-a*1.5)
	}
	for i := range sc.ps {
		p := &sc.ps[i]
		if c.inflow > 0.002 {
			p.u -= dt * c.inflow * (0.35 + 0.5/(0.25+p.u))
			if p.u < 0.03 {
				p.u = 0.92 + 0.14*float64((i*7919)%97)/97
				// Re-entering at the rim on its own arm: particles falling
				// in from there all follow the same spiral stream.
				p.fade, p.fresh, p.spin = 0, true, 0
			}
		}
		p.u += (p.home - p.u) * relax
		p.fade = math.Min(1, p.fade+dt*2.5)
		p.spin += dt * c.swirl * (1/(0.2+p.u) - 0.8)
		if inflN < 0.98 {
			to := math.Round(p.spin/math.Pi) * math.Pi
			p.spin += (to - p.spin) * relax
		}
		if math.Abs(p.spin) > 200 {
			p.spin = math.Mod(p.spin, 2*math.Pi)
		}

		ur := mix(p.u, p.ring, c.ringW)
		wave := math.Sin(11*ur - t*7)
		ur *= 1 + 0.07*c.ripple*wave + 0.02*math.Sin(t*p.twf*0.4+p.tw) + burstPush
		if c.chaos > 0.01 {
			ur += c.chaos * 0.09 * math.Sin(t*8.3+p.tw*7) * math.Sin(t*4.7+p.tw*3)
		}
		urc := math.Max(ur, 0.035)
		armA := p.arm + p.jit*(1-p.focus*inflN) - c.tight*math.Log(urc)
		th := sc.phase + p.spin + mix(armA, p.ringAng, c.ringW)
		if c.chaos > 0.01 {
			th += c.chaos * 0.22 * math.Sin(t*6.1+p.tw*5)
		}
		r := R * ur
		x, y := cx+r*math.Cos(th), center+r*math.Sin(th)

		tw := 1 + c.sparkle*math.Sin(t*p.twf+p.tw)
		b := p.bright * c.bright * pv * p.fade * tw
		b *= 1 + 0.55*c.ripple*wave
		b *= mix(1, smooth(0.03, 0.2, p.u), inflN) // swallowed by the core
		if p.dust {
			b *= 1 - 0.6*inflN // dark lanes between the vortex streams
		}
		b *= 1 + 0.75*c.beam*math.Cos(th-2.6) // the near side of the ring glows
		if b < 0 {
			b = 0
		}
		if p.fresh || pv < 0.01 {
			p.x0, p.y0 = x, y
			p.fresh = false
		} else {
			p.x0, p.y0 = p.x1, p.y1
		}
		p.x1, p.y1, p.b = x, y, b
		p.c = mixc(mixc(c.colA, c.colB, p.tint), white, p.white)
	}
}

// burstLight is how bright the Done flare is now.
func (sc *scene) burstLight() float64 {
	if sc.burst < 0 {
		return 0
	}
	a := sc.t - sc.burst
	if a > 1.4 {
		return 0
	}
	return math.Exp(-a * 3.2)
}

func angDiff(to, from float64) float64 {
	d := math.Mod(to-from, 2*math.Pi)
	if d > math.Pi {
		d -= 2 * math.Pi
	} else if d < -math.Pi {
		d += 2 * math.Pi
	}
	return d
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func smooth(e0, e1, v float64) float64 {
	t := clamp((v - e0) / (e1 - e0))
	return t * t * (3 - 2*t)
}
