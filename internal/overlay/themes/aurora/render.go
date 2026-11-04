package aurora

import "math"

const (
	trailGain = 0.75 // the fading streaks behind the particles
	dotGain   = 0.95 // the particles themselves
	bloomGain = 1.5  // their soft glow
	haloAlpha = 0.86 // the dark halo behind, in the middle
)

// haloTint is the halo's colour: a deep night blue.
var haloTint = [3]float32{0.010, 0.018, 0.030}

var (
	halo   [Canvas * Canvas]float32 // the halo's alpha at each pixel
	radius [Canvas * Canvas]float32 // each pixel's distance from the centre
	edge   [Canvas * Canvas]float32 // light fades out before the window's edge
	bloomK [2*bloomR + 1]float32
	dither [Canvas * Canvas]float32
)

const bloomR = 7 // taps each side, half size pixels

func init() {
	for y := 0; y < Canvas; y++ {
		for x := 0; x < Canvas; x++ {
			r := math.Hypot(float64(x)+0.5-center, float64(y)+0.5-center)
			i := y*Canvas + x
			radius[i] = float32(r)
			edge[i] = float32(1 - smooth(50, center-1, r))
			h := 1 - smooth(26, center-1, r)
			halo[i] = float32(haloAlpha * math.Pow(h, 0.85))
			// A static ±½ step of noise against banding in the soft gradients.
			n := uint32(x)*0x8da6b343 ^ uint32(y)*0xd8163841
			n ^= n >> 13
			n *= 0x85ebca6b
			n ^= n >> 16
			dither[i] = float32(n&0xffff)/0xffff - 0.5
		}
	}
	var sum float32
	for i := range bloomK {
		d := float64(i - bloomR)
		bloomK[i] = float32(math.Exp(-d * d / (2 * 2.6 * 2.6)))
		sum += bloomK[i]
	}
	for i := range bloomK {
		bloomK[i] /= sum
	}
}

// render composes the light, its bloom and the halo into premultiplied BGRA,
// top row first, as UpdateLayeredWindow wants it.
func (sc *scene) render() []byte {
	px := make([]byte, Canvas*Canvas*4)
	if !sc.alive {
		return px
	}
	sc.bloom()
	trail := float32((1 - persist) * trailGain)
	vis := float32(sc.vis)
	// The moment a message arrives a ring of light rushes outward.
	var pulseR, pulseW, pulseI float32
	pulseC := [3]float32{float32(mint[0]), float32(mint[1]), float32(mint[2])}
	if age := sc.t - sc.since; sc.kind == Done && age < 1.2 {
		pulseR = float32(8 + 50*(1-math.Exp(-age*3.5)))
		pulseW = float32(1.5 + 3*age)
		pulseI = float32(0.6*math.Exp(-age*5)) * vis
	}
	hc := haloTint
	for y := 0; y < Canvas; y++ {
		// Half size row above and below for the bilinear bloom.
		hy := float32(y)/2 - 0.25
		y0 := int(math.Floor(float64(hy)))
		fy := hy - float32(y0)
		y1 := min(y0+1, half-1)
		y0 = max(y0, 0)
		for x := 0; x < Canvas; x++ {
			i := y*Canvas + x
			hx := float32(x)/2 - 0.25
			x0 := int(math.Floor(float64(hx)))
			fx := hx - float32(x0)
			x1 := min(x0+1, half-1)
			x0 = max(x0, 0)
			a00, a01 := (y0*half+x0)*3, (y0*half+x1)*3
			a10, a11 := (y1*half+x0)*3, (y1*half+x1)*3
			var l [3]float32
			for j := 0; j < 3; j++ {
				top := sc.small[a00+j] + (sc.small[a01+j]-sc.small[a00+j])*fx
				bot := sc.small[a10+j] + (sc.small[a11+j]-sc.small[a10+j])*fx
				l[j] = sc.acc[i*3+j]*trail + sc.dots[i*3+j]*dotGain + (top+(bot-top)*fy)*bloomGain
			}
			if pulseI > 0 {
				d := (radius[i] - pulseR) / pulseW
				if d > -3 && d < 3 {
					v := pulseI * float32(math.Exp(float64(-d*d)))
					for j := range l {
						l[j] += v * pulseC[j]
					}
				}
			}
			for j := range l {
				l[j] *= edge[i]
			}
			ha := halo[i] * vis
			if ha < 0.0005 && l[0]+l[1]+l[2] < 0.0005 {
				continue
			}
			// Soft clip: bright light runs to white instead of clipping.
			var m [3]float32
			am := float32(0)
			for j := range l {
				m[j] = 1 - float32(math.Exp(float64(-l[j])))
				am = max(am, m[j])
			}
			a := am + ha*(1-am)
			dz := dither[i]
			ab := min(255, max(0, a*255+dz))
			o := i * 4
			for j, off := range [3]int{2, 1, 0} { // BGRA
				c := (m[j] + hc[j]*ha*(1-am)) * 255
				px[o+off] = byte(min(ab, max(0, c+dz)))
			}
			px[o+3] = byte(ab)
		}
	}
	return px
}

// bloom shrinks the light to half size and blurs it.
func (sc *scene) bloom() {
	for y := 0; y < half; y++ {
		for x := 0; x < half; x++ {
			s := ((2*y)*Canvas + 2*x) * 3
			d := (y*half + x) * 3
			for j := 0; j < 3; j++ {
				a := sc.acc[s+j] + sc.acc[s+3+j] + sc.acc[s+Canvas*3+j] + sc.acc[s+Canvas*3+3+j]
				b := sc.dots[s+j] + sc.dots[s+3+j] + sc.dots[s+Canvas*3+j] + sc.dots[s+Canvas*3+3+j]
				sc.tmp[d+j] = 0.25 * (a*(1-persist)*0.6 + b*0.4)
			}
		}
	}
	// Across, into small.
	for y := 0; y < half; y++ {
		for x := 0; x < half; x++ {
			var r, g, b float32
			for k := -bloomR; k <= bloomR; k++ {
				xx := x + k
				if xx < 0 || xx >= half {
					continue
				}
				w := bloomK[k+bloomR]
				s := (y*half + xx) * 3
				r += sc.tmp[s] * w
				g += sc.tmp[s+1] * w
				b += sc.tmp[s+2] * w
			}
			d := (y*half + x) * 3
			sc.small[d], sc.small[d+1], sc.small[d+2] = r, g, b
		}
	}
	// Down, back into tmp, then copied: small ends up blurred both ways.
	for y := 0; y < half; y++ {
		for x := 0; x < half; x++ {
			var r, g, b float32
			for k := -bloomR; k <= bloomR; k++ {
				yy := y + k
				if yy < 0 || yy >= half {
					continue
				}
				w := bloomK[k+bloomR]
				s := (yy*half + x) * 3
				r += sc.small[s] * w
				g += sc.small[s+1] * w
				b += sc.small[s+2] * w
			}
			d := (y*half + x) * 3
			sc.tmp[d], sc.tmp[d+1], sc.tmp[d+2] = r, g, b
		}
	}
	sc.small = sc.tmp
}
