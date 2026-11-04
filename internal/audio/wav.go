package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

const (
	wavPCM        = 1
	wavFloat      = 3
	wavExtensible = 0xFFFE
)

// DecodeWAV returns mono float32 samples and the sample rate. It accepts
// integer PCM (8/16/24/32 bit) and IEEE float, and tolerates the bogus sizes
// streaming writers such as espeak-ng --stdout put in the headers.
func DecodeWAV(b []byte) ([]float32, int, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, 0, errors.New("wav: not a RIFF/WAVE stream")
	}
	var (
		format, channels, bits uint16
		rate                   uint32
		haveFmt                bool
	)
	pos := 12
	for pos+8 <= len(b) {
		id := string(b[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(b[pos+4:]))
		body := pos + 8
		if body+size > len(b) {
			size = len(b) - body
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, 0, errors.New("wav: short fmt chunk")
			}
			format = binary.LittleEndian.Uint16(b[body:])
			channels = binary.LittleEndian.Uint16(b[body+2:])
			rate = binary.LittleEndian.Uint32(b[body+4:])
			bits = binary.LittleEndian.Uint16(b[body+14:])
			if format == wavExtensible && size >= 26 {
				format = binary.LittleEndian.Uint16(b[body+24:])
			}
			haveFmt = true
		case "data":
			if !haveFmt {
				return nil, 0, errors.New("wav: data before fmt")
			}
			pcm, err := decodeSamples(b[body:body+size], format, channels, bits)
			return pcm, int(rate), err
		}
		pos = body + size + size%2
	}
	return nil, 0, errors.New("wav: no data chunk")
}

func decodeSamples(data []byte, format, channels, bits uint16) ([]float32, error) {
	if channels == 0 {
		return nil, errors.New("wav: zero channels")
	}
	width := int(bits) / 8
	if width == 0 {
		return nil, fmt.Errorf("wav: unsupported bit depth %d", bits)
	}
	read, err := sampleReader(format, bits)
	if err != nil {
		return nil, err
	}
	frame := width * int(channels)
	frames := len(data) / frame
	out := make([]float32, frames)
	for i := 0; i < frames; i++ {
		var sum float32
		for c := 0; c < int(channels); c++ {
			sum += read(data[i*frame+c*width:])
		}
		out[i] = sum / float32(channels)
	}
	return out, nil
}

func sampleReader(format, bits uint16) (func([]byte) float32, error) {
	switch {
	case format == wavPCM && bits == 8:
		return func(p []byte) float32 { return (float32(p[0]) - 128) / 128 }, nil
	case format == wavPCM && bits == 16:
		return func(p []byte) float32 { return float32(int16(binary.LittleEndian.Uint16(p))) / 32768 }, nil
	case format == wavPCM && bits == 24:
		return func(p []byte) float32 {
			v := int32(p[0]) | int32(p[1])<<8 | int32(int8(p[2]))<<16
			return float32(v) / 8388608
		}, nil
	case format == wavPCM && bits == 32:
		return func(p []byte) float32 { return float32(int32(binary.LittleEndian.Uint32(p))) / 2147483648 }, nil
	case format == wavFloat && bits == 32:
		return func(p []byte) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(p)) }, nil
	case format == wavFloat && bits == 64:
		return func(p []byte) float32 { return float32(math.Float64frombits(binary.LittleEndian.Uint64(p))) }, nil
	}
	return nil, fmt.Errorf("wav: unsupported format %d/%d-bit", format, bits)
}

// EncodeWAV writes mono 16-bit PCM, used for cached phrases and previews.
func EncodeWAV(pcm []float32, rate int) []byte {
	data := len(pcm) * 2
	b := make([]byte, 44+data)
	copy(b[0:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+data))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], wavPCM)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(data))
	for i, s := range pcm {
		v := max(-1, min(1, s))
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(int16(v*32767)))
	}
	return b
}
