package core

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Tags are voice's own markup inside spoken text: {laugh}, {cough}, {pause:1.5}.
// Agents write only these; voice turns each into what the engine speaking it
// understands (its Expressive.Sounds), or drops it, so a tag is never read
// aloud. {pause} works with every engine: voice makes the silence itself.

// Sounds are the tags voice knows, the non-verbal sounds engines commonly
// support. An engine maps the ones it can make.
var Sounds = []string{"laugh", "chuckle", "giggle", "sigh", "gasp", "cough", "clear_throat", "sniff", "sob", "groan", "yawn", "breath"}

// Moods are the moods a line can carry (mood); an engine maps the ones it can
// show, and one that takes instructions takes any.
var Moods = []string{"neutral", "happy", "excited", "amused", "teasing", "sarcastic", "calm", "tender", "sad", "angry", "afraid", "surprised"}

var (
	reTag = regexp.MustCompile(`\{([A-Za-z_]+)(?::\s*([0-9]+(?:\.[0-9]+)?))?\}`)
	// soundWords: how a sound is written in prose (*laughs*), to its tag.
	soundWords = func() map[string]string {
		m := map[string]string{}
		for _, s := range Sounds {
			m[s] = s
		}
		for w, s := range map[string]string{
			"laughs": "laugh", "laughing": "laugh", "laughter": "laugh", "chuckles": "chuckle", "chuckling": "chuckle",
			"giggles": "giggle", "giggling": "giggle", "sighs": "sigh", "sighing": "sigh", "gasps": "gasp", "gasping": "gasp",
			"coughs": "cough", "coughing": "cough", "sniffs": "sniff", "sniffles": "sniff", "sniffle": "sniff", "sobs": "sob",
			"sobbing": "sob", "groans": "groan", "groaning": "groan", "yawns": "yawn", "yawning": "yawn", "breathes": "breath",
			"inhales": "breath", "exhales": "breath", "ahem": "clear_throat",
		} {
			m[w] = s
		}
		return m
	}()
)

// defaultPause is {pause} without a length, in seconds.
const defaultPause = 0.6

// SoundTag is the tag for a sound written as a word (*laughs*), or "".
func SoundTag(word string) string {
	if s, ok := soundWords[strings.ToLower(strings.TrimSpace(word))]; ok {
		return "{" + s + "}"
	}
	return ""
}

// splitPauses cuts text at its {pause} tags: the pieces, and the silence
// after each (the last piece's is what follows the text).
func splitPauses(text string) (pieces []string, pauses []float64) {
	at := 0
	for _, m := range reTag.FindAllStringSubmatchIndex(text, -1) {
		if !strings.EqualFold(text[m[2]:m[3]], "pause") {
			continue
		}
		secs := defaultPause
		if m[4] >= 0 {
			if v, err := strconv.ParseFloat(text[m[4]:m[5]], 64); err == nil {
				secs = v
			}
		}
		piece := strings.TrimSpace(text[at:m[0]])
		if piece == "" && len(pauses) > 0 {
			pauses[len(pauses)-1] += secs // pauses in a row add up
		} else {
			pieces, pauses = append(pieces, piece), append(pauses, secs)
		}
		at = m[1]
	}
	pieces, pauses = append(pieces, strings.TrimSpace(text[at:])), append(pauses, 0)
	return pieces, pauses
}

// StripTags removes voice's tags, for text that is shown or read back.
func StripTags(s string) string {
	return strings.Join(strings.Fields(reTag.ReplaceAllString(s, " ")), " ")
}

// blankTags hides voice's tags in shown text without moving the words: each
// becomes as many spaces as it has characters.
func blankTags(s string) string {
	return reTag.ReplaceAllStringFunc(s, func(m string) string { return strings.Repeat(" ", len([]rune(m))) })
}

// Expressive is what an engine declares it can do with voice's tags and
// moods, as "expressive" in its engine table (sidecar/backends.json for the
// GPU backends). Emotion says how moods work: none, intensity (each mood
// sets engine settings, Moods), labels (each mood is a token before the
// text, Moods) or instruct (Instruct, with {mood} in it).
type Expressive struct {
	Sounds   map[string]string `json:"sounds,omitempty"`
	Emotion  string            `json:"emotion,omitempty"`
	Moods    map[string]any    `json:"moods,omitempty"`
	Instruct string            `json:"instruct,omitempty"`
}

// ParseExpressive reads an engine table's "expressive" value.
func ParseExpressive(v any) Expressive {
	var x Expressive
	if v != nil {
		if b, err := json.Marshal(v); err == nil {
			json.Unmarshal(b, &x)
		}
	}
	if x.Emotion == "" {
		x.Emotion = "none"
	}
	return x
}

// SoundNames are the sounds it makes, sorted.
func (x Expressive) SoundNames() []string {
	var out []string
	for s := range x.Sounds {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// MoodNames are the moods it shows: all for instructions, else those mapped.
func (x Expressive) MoodNames() []string {
	if x.Emotion == "instruct" && x.Instruct != "" {
		return []string{"any"}
	}
	var out []string
	for m := range x.Moods {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// tagUse counts what became of the tags and moods of one utterance.
type tagUse struct {
	sounds, dropped map[string]bool
	mood            string // applied, ignored or ""
}

func newTagUse() *tagUse { return &tagUse{sounds: map[string]bool{}, dropped: map[string]bool{}} }

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// forEngine turns the tags in text into x's tokens, drops the ones it lacks,
// and applies mood: a token before the text, settings, or an instruction.
func forEngine(x Expressive, text, mood string, u *tagUse) (string, map[string]any) {
	text = reTag.ReplaceAllStringFunc(text, func(m string) string {
		name := strings.ToLower(reTag.FindStringSubmatch(m)[1])
		if tok, ok := x.Sounds[name]; ok && tok != "" {
			if u != nil {
				u.sounds[name] = true
			}
			return " " + tok + " "
		}
		if u != nil {
			u.dropped[name] = true
		}
		return " "
	})
	text = strings.Join(strings.Fields(text), " ")
	var extra map[string]any
	if mood = strings.ToLower(strings.TrimSpace(mood)); mood != "" && mood != "neutral" {
		applied := false
		switch v := x.Moods[mood].(type) {
		case string:
			text, applied = v+" "+text, true
		case map[string]any:
			extra, applied = v, true
		}
		if !applied && x.Emotion == "instruct" && x.Instruct != "" {
			extra, applied = map[string]any{"instruct": strings.ReplaceAll(x.Instruct, "{mood}", mood)}, true
		}
		if u != nil {
			if applied {
				u.mood = "applied"
			} else if u.mood == "" {
				u.mood = "ignored"
			}
		}
	}
	return text, extra
}
