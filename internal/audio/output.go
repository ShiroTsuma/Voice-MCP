// Package audio plays mono float32 PCM and holds the small DSP helpers used
// between engines and the device.
package audio

import (
	"context"
	"os"
	"sync"
)

// DeviceRate is the rate every chunk is converted to before playback.
const DeviceRate = 48000

// Output plays to one device, opened on first use. Device is a
// case-insensitive substring of the device name; empty means the system
// default. With Record set, audio is collected and written to that WAV file
// on Close instead of being played.
type Output struct {
	Device string
	Record string

	once     sync.Once
	dev      *device
	err      error
	mu       sync.Mutex
	recorded []float32
}

func (o *Output) open() error {
	o.once.Do(func() { o.dev, o.err = openDevice(o.Device, DeviceRate) })
	return o.err
}

// Play blocks until pcm (at DeviceRate) has been heard or ctx is cancelled.
func (o *Output) Play(ctx context.Context, pcm []float32) error {
	if len(pcm) == 0 {
		return nil
	}
	if o.Record != "" {
		o.mu.Lock()
		o.recorded = append(o.recorded, pcm...)
		o.mu.Unlock()
		return ctx.Err()
	}
	if err := o.open(); err != nil {
		return err
	}
	return o.dev.play(ctx, pcm)
}

func (o *Output) Close() error {
	if o.Record != "" {
		o.mu.Lock()
		defer o.mu.Unlock()
		return os.WriteFile(o.Record, EncodeWAV(o.recorded, DeviceRate), 0o644)
	}
	if o.dev != nil {
		return o.dev.close()
	}
	return nil
}
