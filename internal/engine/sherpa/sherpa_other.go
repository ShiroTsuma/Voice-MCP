//go:build !windows

package sherpa

import (
	"errors"

	"voice/internal/engine"
)

func init() {
	engine.Register("sherpa", func(string, engine.Config) (engine.Engine, error) {
		return nil, errors.New("sherpa: runtime loading is implemented for Windows only")
	})
}
