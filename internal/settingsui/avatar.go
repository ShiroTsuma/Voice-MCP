package settingsui

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // decoders for uploaded pictures
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

// avatarSize is the side of a stored character picture in pixels.
const avatarSize = 256

// saveAvatar decodes an uploaded picture (a data: URL or plain base64 of a PNG,
// JPEG or GIF), crops it to the middle square, scales it to avatarSize and
// stores it as PNG in dir. It returns the file name, which depends only on the
// picture, so the same picture is stored once.
func saveAvatar(dir, data string) (string, error) {
	if i := strings.Index(data, ","); strings.HasPrefix(data, "data:") && i > 0 {
		data = data[i+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", errors.New("the picture did not arrive whole")
	}
	if len(raw) > 15<<20 {
		return "", errors.New("the picture is over 15 MB")
	}
	// A small file can claim a huge picture: check its size before decoding it.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return "", errors.New("not a PNG, JPEG or GIF picture")
	}
	if cfg.Width > 8000 || cfg.Height > 8000 {
		return "", errors.New("the picture is over 8000 pixels wide or high")
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", errors.New("not a PNG, JPEG or GIF picture")
	}
	out := squareThumb(src, avatarSize)
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	name := hex.EncodeToString(sum[:8]) + ".png"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
		return "", err
	}
	return name, nil
}

// avatarPath is the stored picture called name, or an error for anything that
// is not one of saveAvatar's file names.
func avatarPath(dir, name string) (string, error) {
	if len(name) != 20 || !strings.HasSuffix(name, ".png") {
		return "", fmt.Errorf("no picture %q", name)
	}
	if _, err := hex.DecodeString(strings.TrimSuffix(name, ".png")); err != nil {
		return "", fmt.Errorf("no picture %q", name)
	}
	return filepath.Join(dir, name), nil
}

// squareThumb crops src to its middle square and scales it to side x side,
// averaging the source pixels under each output pixel (smooth when shrinking).
func squareThumb(src image.Image, side int) *image.NRGBA {
	b := src.Bounds()
	n := min(b.Dx(), b.Dy())
	x0, y0 := b.Min.X+(b.Dx()-n)/2, b.Min.Y+(b.Dy()-n)/2
	out := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		sy0, sy1 := y0+y*n/side, y0+max((y+1)*n/side, y*n/side+1)
		for x := 0; x < side; x++ {
			sx0, sx1 := x0+x*n/side, x0+max((x+1)*n/side, x*n/side+1)
			var r, g, bl, a, cnt uint64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					c := color.NRGBAModel.Convert(src.At(sx, sy)).(color.NRGBA)
					// Weighted by alpha, so transparent pixels do not darken edges.
					r += uint64(c.R) * uint64(c.A)
					g += uint64(c.G) * uint64(c.A)
					bl += uint64(c.B) * uint64(c.A)
					a += uint64(c.A)
					cnt++
				}
			}
			if a == 0 {
				continue
			}
			out.SetNRGBA(x, y, color.NRGBA{R: uint8(r / a), G: uint8(g / a), B: uint8(bl / a), A: uint8(a / cnt)})
		}
	}
	return out
}
