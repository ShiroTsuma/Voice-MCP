package aurora

import "math"

// The aurora: cyan, teal and a soft violet, flowing into each other.
var (
	cyan   = rgb{0.30, 0.90, 1.00}
	teal   = rgb{0.10, 0.88, 0.70}
	violet = rgb{0.66, 0.50, 1.00}
	ice    = rgb{0.70, 0.97, 1.00}
	lilac  = rgb{0.80, 0.66, 1.00}
	blue   = rgb{0.32, 0.55, 1.00}
	mint   = rgb{0.42, 1.00, 0.70}
	spring = rgb{0.70, 1.00, 0.82}
	amber  = rgb{1.00, 0.62, 0.16}
	ember  = rgb{1.00, 0.40, 0.20}
	flame  = rgb{1.00, 0.86, 0.55}
)

// aurora is the cyclic cyan → teal → violet gradient at h (any real).
func aurora(h float64) rgb {
	h -= math.Floor(h)
	stops := [...]rgb{cyan, teal, violet, cyan}
	f := h * 3
	i := int(f)
	return mix(stops[i], stops[i+1], smooth(0, 1, f-float64(i)))
}

func mix(a, b rgb, t float64) rgb {
	return rgb{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t, a[2] + (b[2]-a[2])*t}
}

// goal is where a particle wants to be, and how it gets there: a target it is
// pulled to (stiff, 1/s), a velocity it is carried with (fx, fy) and how much
// the curl noise stirs it (amp, pixels a second).
type goal struct {
	tx, ty, fx, fy float64
	stiff, amp     float64
	bright         float64
	col            rgb
}

func (sc *scene) goal(p *particle, i int) goal {
	t, age := sc.t, sc.t-sc.since
	r := math.Hypot(p.x, p.y)
	th := math.Atan2(p.y, p.x)
	var g goal
	switch sc.kind {
	case Recording:
		// A ring of light flowing round; the voice makes it swell, flow
		// faster, billow like a curtain and throw sparks.
		l := sc.level
		bend := noise3(math.Cos(th)*1.2+7.3, math.Sin(th)*1.2, t*(0.45+0.9*l))
		rr := ringR + 3.5*l + (1.0+4.5*l)*bend + 2.2*l*math.Sin(5*th-6*t)
		spread := 1.6 + 2.6*l
		bright := 0.42 + 0.85*l
		off := p.g1
		if p.w > 0.9-0.3*l { // sparks thrown off the ring, mostly outward
			spread *= 2 + 1.2*l
			bright *= 0.55
			off = math.Abs(p.g1) - 0.35
		}
		rt := rr + off*spread
		om := (0.42 + 1.7*l) * (0.8 + 0.4*p.u)
		g = goal{
			tx: rt * math.Cos(th), ty: rt * math.Sin(th),
			fx: -math.Sin(th) * om * r, fy: math.Cos(th) * om * r,
			stiff: 5, amp: 7 + 30*l, bright: bright,
			col: aurora(th/(2*math.Pi) + t*0.07 + p.g2*0.04),
		}
	case Transcribing:
		// Two comets chasing each other round the ring.
		c := float64(i & 1)
		om := 2 * math.Pi * 0.72
		lag := p.u
		a := t*om + c*math.Pi - lag*2.7
		rr := ringR - 1 + p.g1*(0.7+2.6*lag)
		col := mix(ice, teal, smooth(0, 0.7, lag))
		if c == 1 {
			col = mix(lilac, blue, smooth(0, 0.7, lag))
		}
		tx, ty := onRing(th, a, rr, rr*math.Cos(a), rr*math.Sin(a))
		g = goal{
			tx: tx, ty: ty,
			fx: -math.Sin(th) * om * rr, fy: math.Cos(th) * om * rr,
			stiff: 10, amp: 6 + 26*lag,
			bright: 0.06 + 1.25*math.Pow(1-lag, 2.2),
			col:    col,
		}
	case Queued:
		n := min(max(sc.queued, 1), 7)
		om := 0.32
		if p.u < 0.16 {
			// A faint stream along the ring links the knots.
			rt := ringR + p.g1*1.4
			g = goal{
				tx: rt * math.Cos(th), ty: rt * math.Sin(th),
				fx: -math.Sin(th) * (om + 0.2 + 1.2*p.w) * r, fy: math.Cos(th) * (om + 0.2 + 1.2*p.w) * r,
				stiff: 5, amp: 9, bright: 0.3,
				col: aurora(th/(2*math.Pi) + t*0.05),
			}
			break
		}
		k := min(int((p.u-0.16)/0.84*float64(n)), n-1)
		a := t*om + 2*math.Pi*float64(k)/float64(n) - math.Pi/2
		kx, ky := ringR*math.Cos(a), ringR*math.Sin(a)
		spin := t*(1.3+1.2*p.w) + p.ph
		cr := (0.6 + 2.6*math.Abs(p.g1)) * (1 + 1.1/float64(n))
		pulse := math.Pow(math.Max(0, math.Sin(t*2.3-float64(k)*2*math.Pi/float64(n))), 6)
		tx, ty := onRing(th, a, ringR, kx+cr*math.Cos(spin), ky+cr*math.Sin(spin))
		g = goal{
			tx: tx, ty: ty,
			fx: -math.Sin(th) * om * ringR, fy: math.Cos(th) * om * ringR,
			stiff: 7, amp: 7,
			bright: (0.62 + 0.45*pulse) * math.Sqrt(float64(n)/3),
			col:    aurora(float64(k)/float64(n)*0.8 + 0.02 + t*0.03),
		}
	case Done:
		// Everything streams into a check mark inside a closing ring.
		swirl := 4.5 * math.Exp(-age*3)
		if p.u < 0.2 {
			rt := ringR + p.g1*1.2
			om := 0.6 + 1.1*p.w + swirl
			g = goal{
				tx: rt * math.Cos(th), ty: rt * math.Sin(th),
				fx: -math.Sin(th) * om * r, fy: math.Cos(th) * om * r,
				stiff: 5, amp: 8, bright: 0.38,
				col: mix(mint, cyan, 0.5+0.5*math.Sin(th+t)),
			}
			break
		}
		x, y := alongCheck((p.u - 0.2) / 0.8)
		pop := 1 + 0.18*math.Exp(-age*4)*math.Sin(age*13)
		g = goal{
			tx: x*pop + p.g1*1.0, ty: y*pop + p.g2*1.0,
			fx: -math.Sin(th) * swirl * r, fy: math.Cos(th) * swirl * r,
			stiff: 6.5, amp: 5, bright: 0.5,
			col: mix(spring, mint, smooth(0, 1, (p.u-0.2)/0.8)),
		}
	case Problem:
		// A warning sign that shudders as it forms.
		shake := 3.2 * math.Sin(age*36) * math.Exp(-age*2.6)
		var x, y float64
		col, bright := mix(amber, ember, 0.5+0.5*math.Sin(p.u*20+t*2)), 0.9
		switch {
		case p.u < 0.66:
			x, y = alongTriangle(p.u / 0.66)
			x, y = x+p.g1*0.8, y+p.g2*0.8
		case p.u < 0.9:
			x, y = p.g1*0.9, -12+17*(p.u-0.66)/0.24
			col = flame
		default:
			x, y = p.g1*1.1, 11+p.g2*1.1
			col = flame
		}
		g = goal{
			tx: x + shake, ty: y,
			stiff: 8, amp: 5,
			bright: bright * (0.85 + 0.15*math.Sin(age*9)),
			col:    col,
		}
	}
	return g
}

// onRing routes a particle at angle th to its target (tx, ty) near angle a
// along the ring of radius rr rather than across the middle: while it is far,
// it aims a little way ahead of itself on the ring.
func onRing(th, a, rr, tx, ty float64) (float64, float64) {
	const lead = 0.7
	d := math.Remainder(a-th, 2*math.Pi)
	if math.Abs(d) < lead {
		return tx, ty
	}
	b := th + math.Copysign(lead, d)
	return rr * math.Cos(b), rr * math.Sin(b)
}

// alongCheck is the point at q (0..1) along the check mark.
func alongCheck(q float64) (float64, float64) {
	ax, ay, bx, by, cx, cy := -13.0, 0.0, -4.0, 9.0, 14.0, -11.0
	ab := math.Hypot(bx-ax, by-ay)
	bc := math.Hypot(cx-bx, cy-by)
	d := q * (ab + bc)
	if d < ab {
		f := d / ab
		return ax + (bx-ax)*f, ay + (by-ay)*f
	}
	f := (d - ab) / bc
	return bx + (cx-bx)*f, by + (cy-by)*f
}

// alongTriangle is the point at q (0..1) round the warning triangle.
func alongTriangle(q float64) (float64, float64) {
	const rad, cy = 31.0, 4.0
	q *= 3
	e := min(int(q), 2)
	f := q - float64(e)
	a0 := -math.Pi/2 + float64(e)*2*math.Pi/3
	a1 := a0 + 2*math.Pi/3
	x0, y0 := rad*math.Cos(a0), cy+rad*math.Sin(a0)
	x1, y1 := rad*math.Cos(a1), cy+rad*math.Sin(a1)
	return x0 + (x1-x0)*f, y0 + (y1-y0)*f
}

// curl is the curl of 3D Perlin noise taken as a 2D stream function at x, y
// (noise units) and time z: a smooth flow without sources or sinks.
func curl(x, y, z float64) (float64, float64) {
	const e = 0.05
	dy := noise3(x, y+e, z) - noise3(x, y-e, z)
	dx := noise3(x+e, y, z) - noise3(x-e, y, z)
	return dy / (2 * e), -dx / (2 * e)
}

var perm [512]uint8

func init() {
	var p [256]uint8
	for i := range p {
		p[i] = uint8(i)
	}
	r := uint64(0x9E3779B97F4A7C15)
	for i := 255; i > 0; i-- {
		r ^= r << 13
		r ^= r >> 7
		r ^= r << 17
		j := int(r % uint64(i+1))
		p[i], p[j] = p[j], p[i]
	}
	for i := range perm {
		perm[i] = p[i&255]
	}
}

// noise3 is Ken Perlin's improved noise, about -1..1.
func noise3(x, y, z float64) float64 {
	fx, fy, fz := math.Floor(x), math.Floor(y), math.Floor(z)
	X, Y, Z := int(fx)&255, int(fy)&255, int(fz)&255
	x, y, z = x-fx, y-fy, z-fz
	u, v, w := fade(x), fade(y), fade(z)
	A := int(perm[X]) + Y
	AA, AB := int(perm[A])+Z, int(perm[A+1])+Z
	B := int(perm[X+1]) + Y
	BA, BB := int(perm[B])+Z, int(perm[B+1])+Z
	return lerp(w,
		lerp(v, lerp(u, grad(perm[AA], x, y, z), grad(perm[BA], x-1, y, z)),
			lerp(u, grad(perm[AB], x, y-1, z), grad(perm[BB], x-1, y-1, z))),
		lerp(v, lerp(u, grad(perm[AA+1], x, y, z-1), grad(perm[BA+1], x-1, y, z-1)),
			lerp(u, grad(perm[AB+1], x, y-1, z-1), grad(perm[BB+1], x-1, y-1, z-1))))
}

func fade(t float64) float64       { return t * t * t * (t*(t*6-15) + 10) }
func lerp(t, a, b float64) float64 { return a + t*(b-a) }

func grad(hash uint8, x, y, z float64) float64 {
	h := hash & 15
	u := x
	if h >= 8 {
		u = y
	}
	v := z
	if h < 4 {
		v = y
	} else if h == 12 || h == 14 {
		v = x
	}
	if h&1 != 0 {
		u = -u
	}
	if h&2 != 0 {
		v = -v
	}
	return u + v
}
