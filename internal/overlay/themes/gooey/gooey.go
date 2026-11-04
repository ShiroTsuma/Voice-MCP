// Package gooey draws dictation's state as a few soft, gooey blobs that merge
// when they come close: they swirl and swell with the voice while recording,
// chase each other while transcribing, orbit as one satellite per message
// waiting for a busy agent, and flow together into one when a message arrives.
package gooey

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
const Canvas = 96

const maxBlobs = 8

type rgb [3]float64

type blob struct {
	x, y, r float64 // pixels from the centre
	c       rgb
}

// scene eases its blobs toward each state's layout, so every change of state
// flows: blobs drift, swell, shrink to nothing and merge on the way.
type scene struct {
	blobs [maxBlobs]blob
	level float64 // smoothed microphone level
}

var (
	red    = rgb{1.00, 0.27, 0.38}
	coral  = rgb{1.00, 0.50, 0.30}
	pink   = rgb{0.98, 0.32, 0.72}
	violet = rgb{0.62, 0.36, 1.00}
	amber  = rgb{1.00, 0.70, 0.18}
	gold   = rgb{1.00, 0.85, 0.35}
	orange = rgb{0.98, 0.48, 0.12}
	blue   = rgb{0.25, 0.52, 1.00}
	sky    = rgb{0.45, 0.78, 1.00}
	green  = rgb{0.16, 0.80, 0.45}
	mint   = rgb{0.40, 0.95, 0.70}
)

// targets is where each blob should be for kind at time t (seconds).
func (sc *scene) targets(kind Kind, s State, t float64) [maxBlobs]blob {
	var b [maxBlobs]blob
	for i := range b {
		b[i].c = sc.blobs[i].c // vanishing blobs keep their colour
	}
	switch kind {
	case Recording:
		pal := []rgb{red, pink, coral, violet}
		l := sc.level
		for i, c := range pal {
			fi := float64(i)
			dir := 1.0
			if i%2 == 1 {
				dir = -1
			}
			ang := dir*t*(0.9+0.3*fi) + fi*math.Pi/2
			// Louder: the blobs spread out more than they grow, so they pull
			// apart and merge back instead of filling the corner.
			dist := 7 + 9*l + 2.5*math.Sin(t*1.7+fi)
			r := 9.5 + 3.5*l + 1.5*math.Sin(t*2.3+fi*1.3)
			b[i] = blob{dist * math.Cos(ang), dist * math.Sin(ang), r, c}
		}
	case Transcribing:
		pal := []rgb{amber, gold, orange}
		for i, c := range pal {
			fi := float64(i)
			ang := t*4.2 - fi*0.75
			b[i] = blob{16 * math.Cos(ang), 16 * math.Sin(ang), 8.5 - 1.8*fi, c}
		}
		b[3] = blob{0, 0, 6 + math.Sin(t*3), amber}
	case Queued:
		b[0] = blob{0, 0, 14 + math.Sin(t*2), blue}
		n := min(s.Queued, maxBlobs-1)
		for i := 0; i < n; i++ {
			ang := t*0.7 + 2*math.Pi*float64(i)/float64(n)
			b[i+1] = blob{25 * math.Cos(ang), 25 * math.Sin(ang), 6, sky}
		}
	case Done:
		// Everything flows into the middle and becomes one.
		b[0] = blob{0, 0, 16, green}
		for i := 1; i < maxBlobs; i++ {
			b[i] = blob{0, 0, 0, mint}
		}
	case Problem:
		w := 5 * math.Sin(t*10)
		b[0] = blob{w, 0, 12, orange}
		b[1] = blob{-w, 0, 10, coral}
	}
	return b
}

// step moves the blobs a frame (dt seconds) toward kind's layout and reports
// whether anything is still visible.
func (sc *scene) step(kind Kind, s State, t, dt float64) bool {
	sc.level += (s.Level - sc.level) * min(1, dt*12)
	target := sc.targets(kind, s, t)
	k := 1 - math.Exp(-dt*7) // about 150 ms to most of the way
	visible := false
	for i := range sc.blobs {
		cur, to := &sc.blobs[i], target[i]
		if cur.r < 0.5 && to.r > 0 {
			// A blob appearing grows out of the middle, merging out of the rest.
			cur.x, cur.y, cur.c = 0, 0, to.c
		}
		cur.x += (to.x - cur.x) * k
		cur.y += (to.y - cur.y) * k
		cur.r += (to.r - cur.r) * k
		for j := range cur.c {
			cur.c[j] += (to.c[j] - cur.c[j]) * k
		}
		if cur.r > 0.5 {
			visible = true
		}
	}
	return visible
}

// render draws the blobs as one gooey shape (a metaball field cut at 1) with a
// soft glow, into a premultiplied BGRA canvas, top row first, as
// UpdateLayeredWindow wants it.
func (sc *scene) render() []byte {
	px := make([]byte, Canvas*Canvas*4)
	center := Canvas / 2.0
	for y := 0; y < Canvas; y++ {
		for x := 0; x < Canvas; x++ {
			fx, fy := float64(x)+0.5-center, float64(y)+0.5-center
			var f, gx, gy, wsum float64
			var col rgb
			for _, b := range sc.blobs {
				if b.r < 0.5 {
					continue
				}
				dx, dy := fx-b.x, fy-b.y
				// At least a pixel away: right at a centre the field explodes.
				d2 := math.Max(dx*dx+dy*dy, 1)
				v := b.r * b.r / d2
				f += v
				// The field's gradient, for an anti-aliased edge one pixel wide.
				g := -2 * v / d2
				gx += g * dx
				gy += g * dy
				w := v * v // sharper colour regions
				wsum += w
				for j := range col {
					col[j] += w * b.c[j]
				}
			}
			if f < 0.05 {
				continue
			}
			for j := range col {
				col[j] /= wsum
			}
			grad := math.Hypot(gx, gy) + 1e-6
			core := clamp(0.5 + (f-1)/grad)
			glow := 0.22 * smooth(0.25, 1, f)
			// A light sheen toward the top left makes it look round.
			sheen := 0.18 * clamp(-(fx+fy)/(2*center)+0.2) * core
			a := core + glow*(1-core)
			if a <= 0 {
				continue
			}
			i := (y*Canvas + x) * 4
			for j, off := range []int{2, 1, 0} { // BGRA
				v := col[j]*(1-sheen) + sheen
				px[i+off] = byte(255 * clamp(v) * a)
			}
			px[i+3] = byte(255 * clamp(a))
		}
	}
	return px
}

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func smooth(e0, e1, v float64) float64 {
	t := clamp((v - e0) / (e1 - e0))
	return t * t * (3 - 2*t)
}
