// Package look is what every overlay theme shares: the states dictation shows
// and the Scene a theme draws them with. Themes live in overlay/themes/<name>.
package look

import "time"

// Kind is what the overlay shows.
type Kind int

const (
	Hidden       Kind = iota
	Recording         // the microphone is open; react to State.Level
	Transcribing      // a few seconds of work after recording
	Queued            // State.Queued messages (1..7) wait for a busy agent
	Done              // a message was delivered or typed: a short success moment
	Problem           // failed or left on the clipboard: a short warning
	// Speaking: an agent speaks. The overlay draws it as a smaller ring
	// inside the user's (State.Speaking), so every theme shows it.
	Speaking
)

func (k Kind) String() string {
	return [...]string{"hidden", "recording", "transcribing", "queued", "done", "problem", "speaking"}[k]
}

// State is what the overlay should show; the most important part wins.
type State struct {
	Recording    bool
	Level        float64 // microphone level while recording, 0..1
	Transcribing bool
	Queued       int
	Flash        Kind      // Done or Problem, shown until FlashUntil
	FlashUntil   time.Time // then the theme lets it fade
	// Speaking: an agent speaks and nothing else shows who (no popup, no
	// settings window); SpeakLevel is the voice's level 0..1, Tint the
	// speaking character's colour as 0xRRGGBB (0: the default).
	Speaking   bool
	SpeakLevel float64
	Tint       uint32
	// Preparing: an agent's voice is rendering before it is heard; the inner
	// ring shows the theme's working animation meanwhile.
	Preparing bool
}

// Show is the Kind the user's ring shows at now; an agent speaking is the
// inner ring (State.Speaking), drawn beside whatever this is.
func (s State) Show(now time.Time) Kind {
	switch {
	case s.Recording:
		return Recording
	case s.Transcribing:
		return Transcribing
	case s.Flash != Hidden && now.Before(s.FlashUntil):
		return s.Flash
	case s.Queued > 0:
		return Queued
	}
	return Hidden
}

// Scene is one theme's animation. The overlay window calls Step then Render
// once per frame (about 30 a second) on one thread, and hides itself when
// Step reports that nothing is visible.
type Scene interface {
	// Size is the square canvas in pixels.
	Size() int
	// Step advances one frame (dt seconds, t since start) toward kind and
	// reports whether anything is visible.
	Step(kind Kind, s State, t, dt float64) bool
	// Render returns premultiplied BGRA, Size*Size*4 bytes, top row first,
	// transparent where nothing is drawn.
	Render() []byte
}

// Theme names a Scene for the settings.
type Theme struct {
	Name  string // the config value, e.g. "reference"
	Label string // shown in the settings
	New   func() Scene
}
