package audio

import "math"

// Resample converts mono audio with cubic Hermite interpolation, which is
// clean enough for speech without a filter-design dependency. Downsampling
// low-passes first so content above the new Nyquist does not alias.
func Resample(in []float32, from, to int) []float32 {
	if from == to || len(in) == 0 {
		return in
	}
	if to < from {
		in = lowpassFIR(in, 0.45*float64(to)/float64(from))
	}
	ratio := float64(from) / float64(to)
	n := int(float64(len(in)) / ratio)
	out := make([]float32, n)
	at := func(i int) float32 {
		return in[max(0, min(len(in)-1, i))]
	}
	for i := range out {
		pos := float64(i) * ratio
		j := int(pos)
		t := float32(pos - float64(j))
		y0, y1, y2, y3 := at(j-1), at(j), at(j+1), at(j+2)
		c1 := 0.5 * (y2 - y0)
		c2 := y0 - 2.5*y1 + 2*y2 - 0.5*y3
		c3 := 0.5*(y3-y0) + 1.5*(y1-y2)
		out[i] = ((c3*t+c2)*t+c1)*t + y1
	}
	return out
}

// lowpassFIR filters with a Blackman-windowed sinc; cutoff is a fraction of
// the sample rate (0.5 = Nyquist).
func lowpassFIR(in []float32, cutoff float64) []float32 {
	const taps = 63
	var h [taps]float64
	var sum float64
	for i := range h {
		x := float64(i - taps/2)
		s := 2 * cutoff
		if x != 0 {
			s = math.Sin(2*math.Pi*cutoff*x) / (math.Pi * x)
		}
		w := 0.42 - 0.5*math.Cos(2*math.Pi*float64(i)/(taps-1)) + 0.08*math.Cos(4*math.Pi*float64(i)/(taps-1))
		h[i] = s * w
		sum += h[i]
	}
	out := make([]float32, len(in))
	for n := range out {
		var acc float64
		for k := range h {
			if j := n + k - taps/2; j >= 0 && j < len(in) {
				acc += h[k] / sum * float64(in[j])
			}
		}
		out[n] = float32(acc)
	}
	return out
}

// Gain scales samples in place and soft-clips anything that would overflow.
func Gain(pcm []float32, g float64) {
	if g == 1 {
		return
	}
	for i, s := range pcm {
		pcm[i] = softClip(s * float32(g))
	}
}

func softClip(s float32) float32 {
	if s > -0.95 && s < 0.95 {
		return s
	}
	return float32(math.Tanh(float64(s)))
}

// TrimSilence shortens the quiet an engine pads a sentence with, keeping at
// most lead seconds before the first sound and tail seconds after the last,
// so the pause between sentences is the same whatever the engine. Quiet is
// below -40 dBFS over 10 ms; audio that is all quiet comes back as it was.
func TrimSilence(pcm []float32, rate int, lead, tail float64) []float32 {
	const threshold = 0.01
	win := max(1, rate/100)
	loud := func(i int) bool { // the window from sample i
		var sum float64
		n := 0
		for _, s := range pcm[i:min(len(pcm), i+win)] {
			sum += float64(s) * float64(s)
			n++
		}
		return n > 0 && math.Sqrt(sum/float64(n)) >= threshold
	}
	first := -1
	for i := 0; i < len(pcm); i += win {
		if loud(i) {
			first = i
			break
		}
	}
	if first < 0 {
		return pcm
	}
	last := first
	for i := (len(pcm) - 1) / win * win; i > first; i -= win {
		if loud(i) {
			last = i
			break
		}
	}
	from := max(0, first-int(lead*float64(rate)))
	to := min(len(pcm), last+win+int(tail*float64(rate)))
	return pcm[from:to]
}

// Silence returns d seconds of silence at rate.
func Silence(rate int, seconds float64) []float32 {
	return make([]float32, int(float64(rate)*seconds))
}

// Chime renders a short notification sound played before attention and
// question utterances. "none" or an unknown name returns nil.
func Chime(name string, rate int) []float32 {
	type note struct{ freq, dur float64 }
	var notes []note
	switch name {
	case "soft":
		notes = []note{{659.25, 0.11}, {987.77, 0.20}} // E5 -> B5
	case "bell":
		notes = []note{{880, 0.35}}
	case "listen": // the microphone opens
		notes = []note{{523.25, 0.09}, {783.99, 0.16}} // C5 -> G5
	case "heard": // the microphone closes
		notes = []note{{783.99, 0.09}, {523.25, 0.16}}
	default:
		return nil
	}
	var out []float32
	for _, n := range notes {
		samples := int(n.dur * float64(rate))
		for i := 0; i < samples; i++ {
			t := float64(i) / float64(rate)
			attack := math.Min(1, t/0.008)
			decay := math.Exp(-t * 9)
			v := math.Sin(2*math.Pi*n.freq*t) + 0.25*math.Sin(4*math.Pi*n.freq*t)
			out = append(out, float32(0.18*attack*decay*v))
		}
	}
	return append(out, Silence(rate, 0.08)...)
}
