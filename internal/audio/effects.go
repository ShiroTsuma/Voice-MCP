package audio

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Effect processes a stream in order; state carries across calls so one
// utterance split into sentences sounds continuous.
type Effect interface {
	Process(pcm []float32)
}

// Tailer is an effect that keeps ringing after the input ends.
type Tailer interface {
	Tail() []float32
}

// Chain is an ordered list of effects for one utterance.
type Chain []Effect

// ParseChain builds effects from specs such as "lowpass:7500",
// "highpass:90", "compress:soft", "reverb:small" or "gain:-3".
func ParseChain(specs []string, rate int) (Chain, error) {
	var c Chain
	for _, spec := range specs {
		name, arg, _ := strings.Cut(strings.TrimSpace(spec), ":")
		switch name {
		case "lowpass", "highpass":
			hz, err := strconv.ParseFloat(arg, 64)
			if err != nil || hz <= 10 || hz >= float64(rate)/2 {
				return nil, fmt.Errorf("dsp %q: needs a frequency in Hz below %d", spec, rate/2)
			}
			c = append(c, newBiquad(name, hz, rate))
		case "compress":
			e, err := newCompressor(arg, rate)
			if err != nil {
				return nil, fmt.Errorf("dsp %q: %w", spec, err)
			}
			c = append(c, e)
		case "reverb":
			e, err := newReverb(arg, rate)
			if err != nil {
				return nil, fmt.Errorf("dsp %q: %w", spec, err)
			}
			c = append(c, e)
		case "gain":
			db, err := strconv.ParseFloat(arg, 64)
			if err != nil {
				return nil, fmt.Errorf("dsp %q: needs dB", spec)
			}
			c = append(c, gain(math.Pow(10, db/20)))
		default:
			return nil, fmt.Errorf("dsp %q: unknown effect (lowpass, highpass, compress, reverb, gain)", spec)
		}
	}
	return c, nil
}

func (c Chain) Process(pcm []float32) {
	for _, e := range c {
		e.Process(pcm)
	}
}

// Tail flushes ringing effects, passing each tail through the effects after it.
func (c Chain) Tail() []float32 {
	var out []float32
	for i, e := range c {
		if t, ok := e.(Tailer); ok {
			tail := t.Tail()
			for _, later := range c[i+1:] {
				later.Process(tail)
			}
			out = append(out, tail...)
		}
	}
	return out
}

type gain float64

func (g gain) Process(pcm []float32) { Gain(pcm, float64(g)) }

// biquad is an RBJ cookbook low- or high-pass filter with Q = 0.707.
type biquad struct {
	b0, b1, b2, a1, a2 float64
	x1, x2, y1, y2     float64
}

func newBiquad(kind string, hz float64, rate int) *biquad {
	const q = 1 / math.Sqrt2
	w := 2 * math.Pi * hz / float64(rate)
	alpha := math.Sin(w) / (2 * q)
	cos := math.Cos(w)
	var b0, b1, b2 float64
	if kind == "lowpass" {
		b0, b1, b2 = (1-cos)/2, 1-cos, (1-cos)/2
	} else {
		b0, b1, b2 = (1+cos)/2, -(1 + cos), (1+cos)/2
	}
	a0 := 1 + alpha
	return &biquad{b0: b0 / a0, b1: b1 / a0, b2: b2 / a0, a1: -2 * cos / a0, a2: (1 - alpha) / a0}
}

func (f *biquad) Process(pcm []float32) {
	for i, s := range pcm {
		x := float64(s)
		y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
		f.x2, f.x1, f.y2, f.y1 = f.x1, x, f.y1, y
		pcm[i] = float32(y)
	}
}

// compressor evens out loudness, which makes a soft voice sound close
// without peaks jumping out.
type compressor struct {
	threshold, ratio, makeup float64
	attack, release, env     float64
}

func newCompressor(kind string, rate int) (*compressor, error) {
	var thresholdDB, ratio float64
	switch kind {
	case "", "soft":
		thresholdDB, ratio = -20, 2.5
	case "medium":
		thresholdDB, ratio = -24, 4
	case "hard":
		thresholdDB, ratio = -28, 8
	default:
		return nil, fmt.Errorf("mode must be soft, medium or hard")
	}
	coef := func(ms float64) float64 { return math.Exp(-1 / (ms / 1000 * float64(rate))) }
	// Make-up gain restores roughly half of the reduction at full scale.
	makeup := math.Pow(10, -thresholdDB*(1-1/ratio)/40)
	return &compressor{threshold: math.Pow(10, thresholdDB/20), ratio: ratio, makeup: makeup, attack: coef(5), release: coef(120)}, nil
}

func (c *compressor) Process(pcm []float32) {
	for i, s := range pcm {
		x := math.Abs(float64(s))
		if x > c.env {
			c.env = c.attack*c.env + (1-c.attack)*x
		} else {
			c.env = c.release*c.env + (1-c.release)*x
		}
		g := 1.0
		if c.env > c.threshold {
			g = math.Pow(c.env/c.threshold, 1/c.ratio-1)
		}
		pcm[i] = softClip(float32(float64(s) * g * c.makeup))
	}
}

// reverb is a small Schroeder network: parallel combs into series allpasses.
type reverb struct {
	combs     []*delay
	allpasses []*delay
	feedback  float64
	damp      float64
	wet       float64
	tail      int
}

type delay struct {
	buf  []float64
	pos  int
	filt float64
}

func newReverb(kind string, rate int) (*reverb, error) {
	var size, wet float64
	switch kind {
	case "", "small":
		size, wet = 0.6, 0.12
	case "room":
		size, wet = 0.8, 0.16
	case "hall":
		size, wet = 1.2, 0.22
	default:
		return nil, fmt.Errorf("size must be small, room or hall")
	}
	scale := float64(rate) / 44100 * size
	r := &reverb{feedback: 0.70 + 0.1*size, damp: 0.35, wet: wet, tail: int(float64(rate) * 0.35 * size)}
	for _, n := range []int{1116, 1188, 1277, 1356} {
		r.combs = append(r.combs, &delay{buf: make([]float64, int(float64(n)*scale))})
	}
	for _, n := range []int{556, 441} {
		r.allpasses = append(r.allpasses, &delay{buf: make([]float64, int(float64(n)*scale))})
	}
	return r, nil
}

func (r *reverb) sample(x float64) float64 {
	var acc float64
	for _, c := range r.combs {
		y := c.buf[c.pos]
		c.filt = y*(1-r.damp) + c.filt*r.damp
		c.buf[c.pos] = x + c.filt*r.feedback
		c.pos = (c.pos + 1) % len(c.buf)
		acc += y
	}
	acc /= float64(len(r.combs))
	for _, a := range r.allpasses {
		y := a.buf[a.pos]
		a.buf[a.pos] = acc + y*0.5
		a.pos = (a.pos + 1) % len(a.buf)
		acc = y - acc*0.5
	}
	return acc
}

func (r *reverb) Process(pcm []float32) {
	for i, s := range pcm {
		x := float64(s)
		pcm[i] = float32(x*(1-r.wet) + r.sample(x)*r.wet*2)
	}
}

func (r *reverb) Tail() []float32 {
	out := make([]float32, r.tail)
	for i := range out {
		fade := 1 - float64(i)/float64(len(out))
		out[i] = float32(r.sample(0) * r.wet * 2 * fade)
	}
	return out
}
