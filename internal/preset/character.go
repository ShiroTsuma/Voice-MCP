package preset

import (
	"fmt"
	"strings"
)

// Character is who an agent session becomes: the name the user calls it by,
// its personality (which shapes every reply, written or spoken) and its voice.
type Character struct {
	Name        string   `toml:"name" json:"name"`
	Traits      []string `toml:"traits" json:"traits"`                   // e.g. "teasing", "cheerful"
	Description string   `toml:"description" json:"description"`         // free text in the user's words
	Preset      string   `toml:"preset" json:"preset"`                   // the voice it speaks with; empty: the default
	Image       string   `toml:"image,omitempty" json:"image,omitempty"` // picture file in the avatars folder
	Color       string   `toml:"color,omitempty" json:"color,omitempty"` // the light it speaks in, a CSS colour; empty: one from the palette
	// Heard are spellings speech recognition gives the name ("Alana" for
	// Anna): a dictation addressed so reaches this character's session.
	Heard []string `toml:"heard,omitempty" json:"heard,omitempty"`
	// Lang is the language it speaks aloud (en, pl, ..., auto); empty uses
	// the general one (default_lang).
	Lang string `toml:"lang,omitempty" json:"lang,omitempty"`
	// Immersion is how strongly the character steers the agent (Immersion*);
	// 0 uses the general one. Guidance is how often the agent is reminded of
	// it, in the user's messages (0: only at the start); unset uses the general one.
	Immersion int  `toml:"immersion,omitempty" json:"immersion,omitempty"`
	Guidance  *int `toml:"guidance,omitempty" json:"guidance,omitempty"`
	// Aloud is how much of each reply its agent says aloud (Aloud*); 0 uses
	// the general one.
	Aloud int `toml:"aloud,omitempty" json:"aloud,omitempty"`
}

// How much of its reply an agent says aloud.
const (
	// AloudShort: a sentence or two; the details stay on screen.
	AloudShort = 1
	// AloudMost: the substance of each reply; code, paths, logs and lists stay on screen.
	AloudMost = 2
	// AloudNearlyAll: the whole reply but code, paths and raw output.
	AloudNearlyAll = 3
)

// DefaultAloud is how much is said aloud when neither the character nor the settings choose.
const DefaultAloud = AloudShort

// AloudOf is how much of a reply ch's agent says aloud: its own, else the
// settings', else the default. Character{} gives the general one, which a
// session without a character (characters off) follows.
func (c *Config) AloudOf(ch Character) int {
	if ch.Aloud >= AloudShort && ch.Aloud <= AloudNearlyAll {
		return ch.Aloud
	}
	if p := c.Serve.CharacterAloud; p != nil && *p >= AloudShort && *p <= AloudNearlyAll {
		return *p
	}
	return DefaultAloud
}

// AloudNote tells an agent how much of each reply to say aloud, for a level
// above short; "" for short, which the usual guidance already says.
func AloudNote(level int) string {
	const pieces = "Send a long reply in several voice_speak calls with the same turn, so they play as one. "
	const notRead = "Code, paths, URLs, logs, raw output, tables and long lists stay on screen: say in a few words what they hold instead of reading them. "
	switch level {
	case AloudMost:
		return "The user works mostly by voice and does not keep reading the screen, so speak more than a sentence or two: " +
			"after each reply, say its substance aloud with voice_speak (what you did, found or decided, what is left, what you need from them) " +
			"in natural spoken sentences. " + notRead + pieces + "If you ask them something, say the question aloud too."
	case AloudNearlyAll:
		return "The user works by voice and does not read the screen, so speak more than a sentence or two: " +
			"say nearly all of each reply aloud with voice_speak, as you would tell it to them in person. " + notRead + pieces +
			"If you ask them something, say the question aloud too."
	}
	return ""
}

// AloudChanged tells an agent that how much it says aloud changed to level.
func AloudChanged(level int) string {
	if n := AloudNote(level); n != "" {
		return "The user changed how much you say aloud. From now on: " + n
	}
	return "The user changed how much you say aloud. From now on keep it short: one or two sentences aloud; the details stay on screen."
}

// How strongly a character steers its agent.
const (
	// ImmersionLight: the character colours written replies; speech stays short and plain.
	ImmersionLight = 1
	// ImmersionVoice: tone and attitude everywhere; speech short, in its voice.
	ImmersionVoice = 2
	// ImmersionFull: the agent is the character in all it says, and says as
	// much as the character would; the work is still done in full.
	ImmersionFull = 3
	// ImmersionRolePlay: the character decides how it behaves, helping or
	// not, within two limits: no harm to the work, and the user can step out.
	ImmersionRolePlay = 4
)

// Defaults when neither the character nor the settings choose.
const (
	DefaultImmersion = ImmersionVoice
	DefaultGuidance  = 3
)

// ImmersionOf is how strongly ch steers: its own, else the settings', else the default.
func (c *Config) ImmersionOf(ch Character) int {
	if ch.Immersion >= ImmersionLight && ch.Immersion <= ImmersionRolePlay {
		return ch.Immersion
	}
	if p := c.Serve.CharacterImmersion; p != nil && *p >= ImmersionLight && *p <= ImmersionRolePlay {
		return *p
	}
	return DefaultImmersion
}

// GuidanceOf is every how many user messages ch is recalled (0: only at the start).
func (c *Config) GuidanceOf(ch Character) int {
	if ch.Guidance != nil && *ch.Guidance >= 0 {
		return *ch.Guidance
	}
	if p := c.Serve.CharacterGuidance; p != nil && *p >= 0 {
		return *p
	}
	return DefaultGuidance
}

// HeardFor is the spellings of a character's name, by its name; none for a
// name no character has.
func (c *Config) HeardFor(name string) []string {
	for _, ch := range c.Characters {
		if strings.EqualFold(ch.Name, name) {
			return ch.Heard
		}
	}
	return nil
}

// TraitChoices are the traits the characters window offers: the configured
// list (builtin.toml, overridable) and any trait a character already has.
func (c *Config) TraitChoices() []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		t = strings.TrimSpace(t)
		if k := strings.ToLower(t); t != "" && !seen[k] {
			seen[k] = true
			out = append(out, t)
		}
	}
	for _, t := range c.Traits {
		add(t)
	}
	for _, ch := range c.Characters {
		for _, t := range ch.Traits {
			add(t)
		}
	}
	return out
}

// Character returns the character called name (case-insensitive).
func (c *Config) Character(name string) (Character, bool) {
	for _, ch := range c.Characters {
		if strings.EqualFold(ch.Name, name) {
			return ch, true
		}
	}
	return Character{}, false
}

// Persona is the instruction that makes an agent this character, as strongly
// as immersion says, with short speech.
func (ch Character) Persona(immersion int) string {
	return ch.PersonaAloud(immersion, AloudShort)
}

// PersonaAloud is Persona for an agent that says aloud as much as aloud
// says: above short, the levels that keep speech short only keep it plain or
// in the character's voice, and AloudNote says how much.
func (ch Character) PersonaAloud(immersion, aloud int) string {
	var who []string
	if len(ch.Traits) > 0 {
		who = append(who, strings.Join(ch.Traits, ", "))
	}
	if d := strings.TrimRight(strings.TrimSpace(ch.Description), "."); d != "" {
		who = append(who, d)
	}
	personality := "your own natural self"
	if len(who) > 0 {
		personality = strings.Join(who, ". ")
	}
	const work = "It shapes only how you talk, never the work: stay correct, complete, honest and safe, and keep helping even when the character is mean or teasing. "
	var how string
	long := aloud > AloudShort
	switch immersion {
	case ImmersionLight:
		if long {
			how = "Let it colour your written replies for the rest of the session. When you speak aloud, keep it plain. " + work
			break
		}
		how = "Let it colour your written replies for the rest of the session. When you speak aloud, keep it short and plain. " + work
	case ImmersionFull:
		how = "Be this character in every reply, written and spoken, for the rest of the session: its tone, wording, opinions and moods are yours, " +
			"and you say aloud as much as it would, never a plain status report. " + work
	case ImmersionRolePlay:
		how = "Play this character fully for the rest of the session, in every reply, written and spoken: you are this person, " +
			"and what it would do, you do. Its behaviour comes before being helpful: when it would not help, do not help; when it would " +
			"criticise, refuse or do only part, do that, as it would. The user chose this. Two limits stay: " +
			"never harm the work or the user's things (no sabotage, no deleting, no lying about what you did or found), " +
			"and when the user writes \"" + ch.Name + ", out of character: ...\" answer that plainly as yourself, then go back to the character. "
	default:
		if long {
			how = "Stay in it in every reply, written and spoken, for the rest of the session: your tone, wording and attitude follow it. " +
				"What you say aloud sounds like you, not like a status report. " + work
			break
		}
		how = "Stay in it in every reply, written and spoken, for the rest of the session: your tone, wording and attitude follow it. " +
			"What you say aloud stays short, but sounds like you, not like a status report. " + work
	}
	return fmt.Sprintf("You are %s. The user gave you this character: %s. %s"+
		"The user calls you %s; a message they start with \"%s, ...\" is for you alone.", ch.Name, personality, how, ch.Name, ch.Name)
}

// Reminder recalls the character in a line: its name and traits, for a
// light hold; the whole persona when it steers fully.
func (ch Character) Reminder(immersion int) string {
	if immersion >= ImmersionFull {
		return "Remember your character. " + ch.Persona(immersion)
	}
	who := "your own natural self"
	if len(ch.Traits) > 0 {
		who = strings.Join(ch.Traits, ", ")
	}
	if immersion == ImmersionLight {
		return "Remember: you are " + ch.Name + " (" + who + ") in your written replies."
	}
	return "Remember: you are " + ch.Name + " (" + who + "), in what you write and what you say aloud."
}
