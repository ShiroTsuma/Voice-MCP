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
