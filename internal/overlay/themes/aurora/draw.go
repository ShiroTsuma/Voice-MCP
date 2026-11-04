// Package aurora draws dictation's state as an aurora: a few hundred glowing particles carried by a
// smooth curl-noise flow along a ring, leaving faint fading trails over a soft dark
// halo. The ring billows with the voice while recording, gathers into two
// chasing comets while transcribing, into one glowing knot per message
// waiting for a busy agent, flows into a check mark when a message arrives and
// into a warning sign when something fails.
package aurora

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
	nParticles = 560
	center     = Canvas / 2.0
	ringR      = 33.0 // the ring the light flows along, pixels from the centre
	persist    = 0.80 // the share of last frame's light kept: the trails
	half       = Canvas / 2
)

type rgb [3]float64

type particle struct {
	x, y, vx, vy float64 // pixels from the centre, pixels a second
	lx, ly       float64 // last frame's position: the trail is drawn from there
	c            rgb     // current colour, eased toward the state's
	b            float64 // current brightness, eased too
	u, w         float64 // fixed uniform randoms: place in a shape, role
	g1, g2       float64 // fixed gaussian randoms: offsets
	ph, tw       float64 // twinkle phase and rate
	big          bool
}

// scene steers its particles toward each state's shape through the flow, so
// every change of state flows: the light streams from one shape to the next.
type scene struct {
	p      [nParticles]particle
	acc    [Canvas * Canvas * 3]float32 // light with afterimage, linear RGB
	dots   [Canvas * Canvas * 3]float32 // where the particles are this frame
	small  [half * half * 3]float32     // half size light for the bloom
	tmp    [half * half * 3]float32
	alive  bool // particles are out
	seeded bool
	kind   Kind    // what is (or was last) shown
	since  float64 // when kind began
	queued int
	level  float64 // smoothed microphone level
	vis    float64 // 0 hidden .. 1 shown
	t      float64
	rng    uint64
}

// step moves the light a frame (dt seconds) toward kind's shape and reports
// whether anything is still visible.
func (sc *scene) step(kind Kind, s State, t, dt float64) bool {
	if !(dt > 0) || dt > 0.2 {
		dt = 1.0 / 30
	}
	if !sc.alive {
		if kind == Hidden {
			return false
		}
		sc.spawn()
	}
	if kind != Hidden && kind != sc.kind {
		sc.kind, sc.since = kind, t
	}
	if kind == Queued {
		sc.queued = s.Queued
	}
	sc.t = t
	lv := 0.0
	if kind == Recording {
		lv = clamp(s.Level)
	}
	rate := 5.0 // the voice rises fast and settles slower
	if lv > sc.level {
		rate = 22
	}
	sc.level += (lv - sc.level) * (1 - math.Exp(-dt*rate))
	want := 1.0
	if kind == Hidden {
		want = 0
	}
	sc.vis += (want - sc.vis) * (1 - math.Exp(-dt*5.5))
	sc.move(kind == Hidden, dt)
	sc.paint(dt)
	if kind == Hidden && sc.vis < 0.004 {
		sc.alive = false
		clear(sc.acc[:])
		clear(sc.dots[:])
		return false
	}
	return true
}

// spawn puts every particle in the middle, flying out: the light blooms open.
func (sc *scene) spawn() {
	if !sc.seeded {
		sc.rng = 0x2545F4914F6CDD1D
		for i := range sc.p {
			p := &sc.p[i]
			p.u = (float64(i) + sc.rand()) / nParticles
			p.w = sc.rand()
			p.g1, p.g2 = sc.gauss(), sc.gauss()
			p.ph = sc.rand() * 2 * math.Pi
			p.tw = 1.5 + 4*sc.rand()
			p.big = sc.rand() < 0.16
		}
		sc.seeded = true
	}
	for i := range sc.p {
		p := &sc.p[i]
		a := sc.rand() * 2 * math.Pi
		r := 1 + 4*sc.rand()
		sp := 30 + 70*sc.rand()
		p.x, p.y = r*math.Cos(a), r*math.Sin(a)
		p.lx, p.ly = p.x, p.y
		p.vx, p.vy = sp*math.Cos(a), sp*math.Sin(a)
		p.b = 0
		p.c = rgb{}
	}
	sc.alive, sc.vis, sc.level = true, 0, 0
	clear(sc.acc[:])
}

func (sc *scene) rand() float64 {
	sc.rng ^= sc.rng << 13
	sc.rng ^= sc.rng >> 7
	sc.rng ^= sc.rng << 17
	return float64(sc.rng>>11) / (1 << 53)
}

func (sc *scene) gauss() float64 {
	return math.Sqrt(-2*math.Log(1-sc.rand())) * math.Cos(2*math.Pi*sc.rand())
}

// move steers every particle toward its goal, carried by the curl noise flow.
func (sc *scene) move(fading bool, dt float64) {
	steer := 1 - math.Exp(-dt*9)
	kc := 1 - math.Exp(-dt*3.5)
	kb := 1 - math.Exp(-dt*5)
	z := sc.t * 0.23
	fresh := sc.p[0].c == rgb{}
	for i := range sc.p {
		p := &sc.p[i]
		g := sc.goal(p, i)
		cx, cy := curl(p.x/24, p.y/24, z)
		vx := g.fx + (g.tx-p.x)*g.stiff + cx*g.amp
		vy := g.fy + (g.ty-p.y)*g.stiff + cy*g.amp
		if fading {
			// Leaving: the light drifts apart as it dims.
			r := math.Hypot(p.x, p.y) + 1e-6
			out := 40 * (1 - sc.vis)
			vx += p.x / r * out
			vy += p.y / r * out
		}
		p.vx += (vx - p.vx) * steer
		p.vy += (vy - p.vy) * steer
		if sp := math.Hypot(p.vx, p.vy); sp > 300 {
			p.vx, p.vy = p.vx*300/sp, p.vy*300/sp
		}
		p.lx, p.ly = p.x, p.y
		p.x += p.vx * dt
		p.y += p.vy * dt
		if fresh {
			p.c = g.col
		}
		for j := range p.c {
			p.c[j] += (g.col[j] - p.c[j]) * kc
		}
		p.b += (g.bright - p.b) * kb
	}
}

// paint fades last frame's light a little and draws every particle as a short
// streak from where it was to where it is.
func (sc *scene) paint(dt float64) {
	d := float32(math.Pow(persist, dt*30))
	for i := range sc.acc {
		sc.acc[i] *= d
	}
	clear(sc.dots[:])
	for i := range sc.p {
		p := &sc.p[i]
		in := p.b * sc.vis * (0.75 + 0.25*math.Sin(sc.t*p.tw+p.ph))
		if in < 0.002 {
			continue
		}
		dx, dy := p.x-p.lx, p.y-p.ly
		n := min(int(math.Hypot(dx, dy)/1.1)+1, 7)
		w := float32(in / float64(n))
		k := &kernSmall
		if p.big {
			k = &kernBig
		}
		c := [3]float32{float32(p.c[0]) * w, float32(p.c[1]) * w, float32(p.c[2]) * w}
		for s := 1; s <= n; s++ {
			f := float64(s) / float64(n)
			splat(&sc.acc, p.lx+dx*f+center, p.ly+dy*f+center, c, k)
		}
		c = [3]float32{float32(p.c[0] * in), float32(p.c[1] * in), float32(p.c[2] * in)}
		splat(&sc.dots, p.x+center, p.y+center, c, k)
	}
}

const (
	kernN     = 128
	kernReach = 2.4 * 2.4 // squared pixels
)

var kernSmall, kernBig [kernN + 1]float32

func init() {
	for i := range kernSmall {
		d2 := float64(i) / kernN * kernReach
		edge := 1 - float64(i)/kernN // reaches zero at the edge: no square cut
		kernSmall[i] = float32(math.Exp(-d2/(2*0.62*0.62)) * edge)
		kernBig[i] = float32(1.25 * math.Exp(-d2/(2*0.95*0.95)) * edge)
	}
}

// splat adds one soft dot of light at canvas position x, y.
func splat(buf *[Canvas * Canvas * 3]float32, x, y float64, c [3]float32, k *[kernN + 1]float32) {
	x0, y0 := int(math.Floor(x-2)), int(math.Floor(y-2))
	for yy := max(y0, 0); yy < min(y0+5, Canvas); yy++ {
		dy := float64(yy) + 0.5 - y
		for xx := max(x0, 0); xx < min(x0+5, Canvas); xx++ {
			dx := float64(xx) + 0.5 - x
			d2 := dx*dx + dy*dy
			if d2 >= kernReach {
				continue
			}
			v := k[int(d2*(kernN/kernReach))]
			i := (yy*Canvas + xx) * 3
			buf[i] += c[0] * v
			buf[i+1] += c[1] * v
			buf[i+2] += c[2] * v
		}
	}
}

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func smooth(e0, e1, v float64) float64 {
	t := clamp((v - e0) / (e1 - e0))
	return t * t * (3 - 2*t)
}
