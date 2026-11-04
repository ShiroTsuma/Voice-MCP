//go:build !windows

package sapi

import (
	"errors"

	"voice/internal/engine"
)

func init() {
	engine.Register("sapi", func(string, engine.Config) (engine.Engine, error) {
		return nil, errors.New("sapi: available only on Windows")
	})
}
