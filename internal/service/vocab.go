package service

import (
	"regexp"
	"strings"

	"voice/internal/preset"
)

// Vocabulary corrects what the recognizer wrote for words it does not know:
// names of characters and projects, and the user's own words. A word, or two
// or three in a row, that sounds like an entry becomes the entry as it is
// written ("Rebecca" → "Rebekka", "MCP tools" → "MCP-Tools"); a "heard ->
// meant" pair replaces what sound alone cannot match ("Rytmi" → "README").
// It works with any recognizer, after recognition.
type Vocabulary struct {
	words   []vocabWord
	replace []replacement
}

type vocabWord struct {
	text  string // as written
	plain string // normalized, words joined
	n     int    // how many spoken words it spans
}

type replacement struct {
	re   *regexp.Regexp
	with string
}

// NewVocabulary builds one from words and "heard -> meant" lines; blank and
// duplicate entries are ignored.
func NewVocabulary(entries []string) *Vocabulary {
	v := &Vocabulary{}
	seen := map[string]bool{}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if heard, meant, ok := strings.Cut(e, "->"); ok {
			heard, meant = strings.TrimSpace(heard), strings.TrimSpace(meant)
			if heard == "" || meant == "" {
				continue
			}
			v.replace = append(v.replace, replacement{
				re:   regexp.MustCompile(`(?i)(^|[^\pL\pN])` + regexp.QuoteMeta(heard) + `($|[^\pL\pN])`),
				with: meant,
			})
			continue
		}
		plain := ""
		for _, w := range spoken(e) {
			plain += normalize(w)
		}
		if plain == "" || seen[plain] {
			continue
		}
		seen[plain] = true
		v.words = append(v.words, vocabWord{text: e, plain: plain, n: max(1, len(spoken(e)))})
	}
	return v
}

// spoken splits an entry the way it is said: "MCP-Tools" is two words.
func spoken(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '-' || r == '_' || r == '.' })
}

var wordRe = regexp.MustCompile(`[\pL\pN']+`)

// Correct returns text with the vocabulary applied.
func (v *Vocabulary) Correct(text string) string {
	if v == nil || text == "" {
		return text
	}
	for _, r := range v.replace {
		text = r.re.ReplaceAllString(text, "${1}"+strings.ReplaceAll(r.with, "$", "$$")+"${2}")
	}
	if len(v.words) == 0 {
		return text
	}
	locs := wordRe.FindAllStringIndex(text, -1)
	var out strings.Builder
	last := 0
	for i := 0; i < len(locs); {
		matched := false
		// Longer entries first: "MCP tools" before "tools".
		for n := 3; n >= 1 && !matched; n-- {
			if i+n > len(locs) {
				continue
			}
			plain := ""
			for _, l := range locs[i : i+n] {
				plain += normalize(text[l[0]:l[1]])
			}
			for _, w := range v.words {
				if w.n != n || !vocabMatch(plain, w.plain) {
					continue
				}
				out.WriteString(text[last:locs[i][0]])
				out.WriteString(w.text)
				last = locs[i+n-1][1]
				i += n
				matched = true
				break
			}
		}
		if !matched {
			i++
		}
	}
	out.WriteString(text[last:])
	return out.String()
}

// vocabMatch: the same word, or one that sounds like it. Short words must
// match exactly, so "and" never turns into "Anna".
func vocabMatch(heard, entry string) bool {
	if heard == entry {
		return true
	}
	if len([]rune(entry)) < 5 || len([]rune(heard)) < 4 {
		return false
	}
	return close(heard, entry)
}

// VocabularyFor is the vocabulary dictation and listening use now: the
// user's words, the characters' names, the sessions' names and projects.
func VocabularyFor(cfg *preset.Config, agents *Agents) *Vocabulary {
	var entries []string
	if cfg != nil {
		entries = append(entries, cfg.Listen.Words...)
		for _, ch := range cfg.Characters {
			entries = append(entries, ch.Name)
		}
	}
	for _, a := range agents.Info() {
		entries = append(entries, a.Name)
		// A project named like an identifier (MCP-Tools, isaac64); a plain word
		// ("recipes") would bend ordinary speech toward it.
		if a.Project != strings.ToLower(a.Project) || strings.ContainsAny(a.Project, "-_.0123456789") {
			entries = append(entries, a.Project)
		}
	}
	return NewVocabulary(entries)
}
