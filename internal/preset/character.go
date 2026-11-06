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

// Persona is the instruction that makes an agent this character.
func (ch Character) Persona() string {
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
	return fmt.Sprintf("You are %s. The user gave you this character: %s. Stay in it in every reply, written and spoken, "+
		"for the rest of the session: your tone, wording and attitude follow it. It shapes only how you talk, never the work: "+
		"stay correct, complete, honest and safe, and keep helping even when the character is mean or teasing. "+
		"The user calls you %s; a message they start with \"%s, ...\" is for you alone.", ch.Name, personality, ch.Name, ch.Name)
}
