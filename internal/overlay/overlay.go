// Package overlay shows dictation's state in the bottom right corner of the
// screen, in a click-through window that never takes focus, drawn by one of
// the themes in overlay/themes (chosen with [serve] dictate_theme).
package overlay

import (
	"strings"

	"voice/internal/overlay/look"
	"voice/internal/overlay/themes/aurora"
	"voice/internal/overlay/themes/comets"
	"voice/internal/overlay/themes/galaxy"
	"voice/internal/overlay/themes/gooey"
	"voice/internal/overlay/themes/liquid"
	"voice/internal/overlay/themes/reference"
	"voice/internal/overlay/themes/swarm"
	"voice/internal/overlay/themes/torus"
	"voice/internal/overlay/themes/waveform"
)

// Logf, when set, notes when the overlay appears and where.
var Logf func(format string, args ...any)

// The shared states, re-exported for the rest of voice.
type (
	Kind  = look.Kind
	State = look.State
)

const (
	Hidden       = look.Hidden
	Recording    = look.Recording
	Speaking     = look.Speaking
	Transcribing = look.Transcribing
	Queued       = look.Queued
	Done         = look.Done
	Problem      = look.Problem
)

// Themes are the looks to choose from, the default first.
var Themes = []look.Theme{
	{Name: "reference", Label: "Particle ring", New: reference.New},
	{Name: "torus", Label: "3D torus", New: torus.New},
	{Name: "waveform", Label: "Voice waveform", New: waveform.New},
	{Name: "galaxy", Label: "Galaxy", New: galaxy.New},
	{Name: "swarm", Label: "Firefly swarm", New: swarm.New},
	{Name: "liquid", Label: "Liquid core and sparks", New: liquid.New},
	{Name: "comets", Label: "Comet ring (minimal)", New: comets.New},
	{Name: "aurora", Label: "Aurora", New: aurora.New},
	{Name: "gooey", Label: "Gooey blobs", New: gooey.New},
}

// theme is the theme called name, or the default one.
func theme(name string) look.Theme {
	for _, t := range Themes {
		if strings.EqualFold(t.Name, name) {
			return t
		}
	}
	return Themes[0]
}
