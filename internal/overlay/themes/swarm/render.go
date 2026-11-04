package swarm

import "math"

// Each mote is drawn as a small white-hot core inside a wider tinted halo, both
// gaussian, added into a light buffer. A much wider, coarse "shadow" of the
// swarm goes into a dark buffer so the glow reads on light desktops too.
const (
	darkCell = 4 // pixels per cell of the dark buffer
	darkSide = Canvas / darkCell
	gaussRes = 64 // lookup entries per unit of d²/σ²
	gaussMax = 9  // d²/σ² beyond which a gaussian is treated as zero
	toneRes  = 256
	toneMax  = 8.0
)

var (
	gaussLUT [gaussMax*gaussRes + 1]float32
	toneLUT  [int(toneMax*toneRes) + 1]float32
	darkTint = rgb{0.03, 0.05, 0.10}
)

func init() {
	for i := range gaussLUT {
		gaussLUT[i] = float32(math.Exp(-float64(i) / gaussRes))
	}
	for i := range toneLUT {
		toneLUT[i] = float32(1 - math.Exp(-float64(i)/toneRes))
	}
}

func gauss(q float32) float32 { // q = d²/σ²
	i := int(q * gaussRes)
	if i >= len(gaussLUT) {
		return 0
	}
	return gaussLUT[i]
}

// tone squashes added light into 0..1 so that overlapping motes burn to white
// softly instead of clipping.
func tone(v float32) float32 {
	i := int(v * toneRes)
	if i >= len(toneLUT) {
		return 1
	}
	return toneLUT[i]
}

// render draws the swarm into a premultiplied BGRA canvas, top row first, as
// UpdateLayeredWindow wants it.
func (sc *scene) render() []byte {
	px := make([]byte, Canvas*Canvas*4)
	if !sc.ready || sc.life <= 0 {
		return px
	}
	if sc.light == nil {
		sc.light = make([]float32, Canvas*Canvas*3)
		sc.dark = make([]float32, darkSide*darkSide)
	}
	clear(sc.light)
	clear(sc.dark)
	const half = Canvas / 2.0
	const edge = half - 10 // motes fade out before they could clip at the edge
	tint := sc.col
	for i := range sc.p {
		p := &sc.p[i]
		d := math.Hypot(p.x, p.y)
		fade := 1 - smooth(edge-12, edge, d)
		l := sc.life * p.lum * fade * (1 + sc.glow)
		if l < 0.004 {
			continue
		}
		sc.drawMote(p, float32(l), tint)
		sc.shade(p.x+half, p.y+half, float32(l*p.size))
	}
	sc.compose(px)
	return px
}

func (sc *scene) drawMote(p *mote, l float32, tint rgb) {
	const half = Canvas / 2.0
	cx, cy := p.x+half, p.y+half
	sc2 := float32(0.75 + 0.45*p.size)
	sc2 *= sc2
	sh := 2.4 + 1.6*p.size
	sh2 := float32(sh * sh)
	ac := 2.4 * l
	ah := 0.13 * l * float32(p.size)
	// Core: tinted toward white; halo: the tint.
	w := 0.5 + 0.5*p.white
	var core, halo [3]float32
	for j := range core {
		core[j] = ac * float32(tint[j]+(1-tint[j])*w)
		halo[j] = ah * float32(tint[j])
	}
	r := sh * 3
	x0, x1 := max(0, int(cx-r)), min(Canvas-1, int(cx+r+1))
	y0, y1 := max(0, int(cy-r)), min(Canvas-1, int(cy+r+1))
	for y := y0; y <= y1; y++ {
		dy := float32(float64(y) + 0.5 - cy)
		row := sc.light[y*Canvas*3 : (y+1)*Canvas*3]
		for x := x0; x <= x1; x++ {
			dx := float32(float64(x) + 0.5 - cx)
			d2 := dx*dx + dy*dy
			gh := gauss(d2 / sh2)
			if gh == 0 {
				continue
			}
			gc := gauss(d2 / sc2)
			o := x * 3
			row[o] += core[0]*gc + halo[0]*gh
			row[o+1] += core[1]*gc + halo[1]*gh
			row[o+2] += core[2]*gc + halo[2]*gh
		}
	}
}

// shade adds a wide, soft darkness under a mote into the coarse dark buffer.
func (sc *scene) shade(cx, cy float64, l float32) {
	const sigma = 1.7 // cells
	const sigma2 = sigma * sigma
	gx, gy := cx/darkCell-0.5, cy/darkCell-0.5
	const r = 5
	x0, x1 := max(0, int(gx-r)), min(darkSide-1, int(gx+r+1))
	y0, y1 := max(0, int(gy-r)), min(darkSide-1, int(gy+r+1))
	for y := y0; y <= y1; y++ {
		dy := float32(float64(y) - gy)
		for x := x0; x <= x1; x++ {
			dx := float32(float64(x) - gx)
			sc.dark[y*darkSide+x] += l * gauss((dx*dx+dy*dy)/sigma2)
		}
	}
}

// darkAt samples the dark buffer bilinearly at a pixel.
func (sc *scene) darkAt(x, y int) float32 {
	fx := (float32(x)+0.5)/darkCell - 0.5
	fy := (float32(y)+0.5)/darkCell - 0.5
	if fx < 0 {
		fx = 0
	}
	if fy < 0 {
		fy = 0
	}
	ix, iy := int(fx), int(fy)
	if ix >= darkSide-1 {
		ix, fx = darkSide-2, float32(darkSide-1)
	}
	if iy >= darkSide-1 {
		iy, fy = darkSide-2, float32(darkSide-1)
	}
	tx, ty := fx-float32(ix), fy-float32(iy)
	d := sc.dark
	a := d[iy*darkSide+ix]*(1-tx) + d[iy*darkSide+ix+1]*tx
	b := d[(iy+1)*darkSide+ix]*(1-tx) + d[(iy+1)*darkSide+ix+1]*tx
	return a*(1-ty) + b*ty
}

// compose lays the light over the shadow and writes premultiplied BGRA.
func (sc *scene) compose(px []byte) {
	shadow := float32(0.4 * sc.life)
	dt := [3]float32{float32(darkTint[0]), float32(darkTint[1]), float32(darkTint[2])}
	for y := 0; y < Canvas; y++ {
		for x := 0; x < Canvas; x++ {
			i := y*Canvas + x
			lr, lg, lb := tone(sc.light[i*3]), tone(sc.light[i*3+1]), tone(sc.light[i*3+2])
			al := max(lr, lg, lb)
			ad := shadow * (1 - gauss(sc.darkAt(x, y)*0.5)) // 1 - e^(-k·dark)
			if al < 0.002 && ad < 0.002 {
				continue
			}
			bg := (1 - al) * ad
			r := lr + bg*dt[0]
			g := lg + bg*dt[1]
			b := lb + bg*dt[2]
			a := al + bg
			o := i * 4
			px[o] = byte(b*255 + 0.5)
			px[o+1] = byte(g*255 + 0.5)
			px[o+2] = byte(r*255 + 0.5)
			px[o+3] = byte(a*255 + 0.5)
		}
	}
}

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func smooth(e0, e1, v float64) float64 {
	t := clamp((v - e0) / (e1 - e0))
	return t * t * (3 - 2*t)
}
