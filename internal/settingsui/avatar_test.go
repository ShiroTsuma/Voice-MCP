package settingsui

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

func TestSaveAvatarCropsAndScales(t *testing.T) {
	// A wide picture: red left half, blue right half.
	src := image.NewNRGBA(image.Rect(0, 0, 600, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 600; x++ {
			c := color.NRGBA{R: 255, A: 255}
			if x >= 300 {
				c = color.NRGBA{B: 255, A: 255}
			}
			src.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, src)
	dir := t.TempDir()
	name, err := saveAvatar(dir, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	path, err := avatarPath(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(path)
	defer f.Close()
	got, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if b := got.Bounds(); b.Dx() != avatarSize || b.Dy() != avatarSize {
		t.Fatalf("size %v", b)
	}
	// The middle square spans both halves: red on the left, blue on the right.
	if r, _, _, _ := got.At(10, 128).RGBA(); r>>8 < 250 {
		t.Error("left side not red")
	}
	if _, _, b, _ := got.At(245, 128).RGBA(); b>>8 < 250 {
		t.Error("right side not blue")
	}
	if _, err := avatarPath(dir, "../settings.toml"); err == nil {
		t.Error("a path outside the pictures was accepted")
	}
	if _, err := saveAvatar(dir, "bm90IGFuIGltYWdl"); err == nil {
		t.Error("not a picture, accepted")
	}
}
