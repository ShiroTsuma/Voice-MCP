package overlay

// scaled resizes a square premultiplied BGRA image from side to out pixels
// with bilinear filtering (premultiplied channels interpolate correctly).
// The same size returns px as it is.
func scaled(px []byte, side, out int) []byte {
	if out == side || side <= 1 {
		return px
	}
	dst := make([]byte, out*out*4)
	ratio := float64(side) / float64(out)
	for y := 0; y < out; y++ {
		fy := (float64(y)+0.5)*ratio - 0.5
		y0 := int(fy)
		if fy < 0 {
			y0, fy = 0, 0
		}
		y1 := min(y0+1, side-1)
		wy := fy - float64(y0)
		for x := 0; x < out; x++ {
			fx := (float64(x)+0.5)*ratio - 0.5
			x0 := int(fx)
			if fx < 0 {
				x0, fx = 0, 0
			}
			x1 := min(x0+1, side-1)
			wx := fx - float64(x0)
			a, b := (y0*side+x0)*4, (y0*side+x1)*4
			c, d := (y1*side+x0)*4, (y1*side+x1)*4
			o := (y*out + x) * 4
			for ch := 0; ch < 4; ch++ {
				top := float64(px[a+ch])*(1-wx) + float64(px[b+ch])*wx
				bot := float64(px[c+ch])*(1-wx) + float64(px[d+ch])*wx
				dst[o+ch] = byte(top*(1-wy) + bot*wy + 0.5)
			}
		}
	}
	return dst
}

// defaultTint colours speech when the speaking character has no colour.
const defaultTint = 0xA99BFF

// tinted recolours premultiplied BGRA pixels to tint, keeping each pixel's
// brightness and alpha: one theme's animation, in a character's colour.
func tinted(px []byte, tint uint32) []byte {
	out := make([]byte, len(px))
	tr, tg, tb := float64(tint>>16&0xff), float64(tint>>8&0xff), float64(tint&0xff)
	for i := 0; i+3 < len(px); i += 4 {
		a := float64(px[i+3])
		if a == 0 {
			continue
		}
		// The pixel's own brightness (not premultiplied), the tint at that
		// brightness lifted a little, then premultiplied again; a bright
		// core keeps some white so highlights still shine.
		b, g, r := float64(px[i]), float64(px[i+1]), float64(px[i+2])
		lum := min(1, (0.114*b+0.587*g+0.299*r)/a*1.35)
		white := max(0, lum-0.8) * 2.5 * 0.5
		mix := func(t float64) byte {
			v := (t*(1-white) + 255*white) * lum * a / 255
			return byte(min(a, v))
		}
		out[i], out[i+1], out[i+2], out[i+3] = mix(tb), mix(tg), mix(tr), px[i+3]
	}
	return out
}

// innerShare is the agent's ring's size against the user's.
const innerShare = 0.55

// within draws small (a side x side image) at share of its size in the
// middle of px, over it; both premultiplied BGRA.
func within(px, small []byte, side int, share float64) []byte {
	n := int(float64(side) * share)
	if n < 2 {
		return px
	}
	sm := scaled(small, side, n)
	out := append([]byte(nil), px...)
	off := (side - n) / 2
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			s := (y*n + x) * 4
			a := int(sm[s+3])
			if a == 0 {
				continue
			}
			d := ((y+off)*side + x + off) * 4
			for k := 0; k < 4; k++ {
				out[d+k] = byte(min(255, int(sm[s+k])+int(out[d+k])*(255-a)/255))
			}
		}
	}
	return out
}
