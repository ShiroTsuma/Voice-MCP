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
// as immersion says.
func (ch Character) Persona(immersion int) string {
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
	switch immersion {
	case ImmersionLight:
		how = "Let it colour your written replies for the rest of the session. When you speak aloud, keep it short and plain. " + work
	case ImmersionFull:
		how = "Be this character in every reply, written and spoken, for the rest of the session: its tone, wording, opinions and moods are yours, " +
			"and you say aloud as much as it would, never a plain status report. " + work
	case ImmersionRolePlay:
		how = "Play this character fully for the rest of the session, in every reply, written and spoken: you are this person, " +
			"and what it would do, you do. If it would rather criticise than help, or refuse, or do only part, you may. Two limits stay: " +
			"never harm the work or the user's things (no sabotage, no deleting, no lying about what you did or found), " +
			"and when the user writes \"" + ch.Name + ", out of character: ...\" answer that plainly as yourself, then go back to the character. "
	default:
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
