package core

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	reFence      = regexp.MustCompile("(?s)```.*?(```|$)")
	reInlineCode = regexp.MustCompile("`([^`\n]*)`")
	reImage      = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	reLink       = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	reURL        = regexp.MustCompile(`(?i)\b(?:https?://|www\.)\S+`)
	reHeading    = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s+`)
	reBullet     = regexp.MustCompile(`(?m)^\s*(?:[-*+>]|\d+[.)])\s+`)
	reEmphasis   = regexp.MustCompile(`(\*\*|__|~~)(.+?)(\*\*|__|~~)`)
	reStar       = regexp.MustCompile(`(^|\s)\*([^*\n]+)\*`)
	reTableRule  = regexp.MustCompile(`(?m)^\s*\|?[\s:|-]+\|[\s:|-]*$`)
	reSpaces     = regexp.MustCompile(`[ \t]+`)
)

// maxInlineCode is the longest `code` span still read aloud; longer spans are
// identifiers or commands nobody wants spelled out.
const maxInlineCode = 32

// Clean turns model markdown into speakable text: code blocks, URLs and markup
// are dropped, line breaks become sentence ends.
func Clean(s string) string {
	s = reFence.ReplaceAllString(s, " ")
	s = reInlineCode.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[1 : len(m)-1]
		if utf8.RuneCountInString(inner) > maxInlineCode {
			return " "
		}
		return inner
	})
	s = reImage.ReplaceAllString(s, " ")
	s = reLink.ReplaceAllString(s, "$1")
	s = reURL.ReplaceAllString(s, " ")
	s = reTableRule.ReplaceAllString(s, "")
	s = reHeading.ReplaceAllString(s, "")
	s = reBullet.ReplaceAllString(s, "")
	s = reEmphasis.ReplaceAllString(s, "$2")
	// *laughs* is a sound, made where the engine can and never read; other
	// *emphasis* is read as the words.
	s = reStar.ReplaceAllStringFunc(s, func(m string) string {
		sm := reStar.FindStringSubmatch(m)
		if tag := SoundTag(sm[2]); tag != "" {
			return sm[1] + tag
		}
		return sm[1] + sm[2]
	})
	s = strings.ReplaceAll(s, "|", ", ")

	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.Is(unicode.So, r), unicode.Is(unicode.Cs, r), r == '️', r == '‍':
			// Emoji and pictographs: engines read their names or garble them.
		case r == '\r':
		default:
			b.WriteRune(r)
		}
	}

	var lines []string
	for _, line := range strings.Split(b.String(), "\n") {
		line = strings.TrimSpace(reSpaces.ReplaceAllString(line, " "))
		if line == "" {
			continue
		}
		if !endsSentence(line) {
			line += "."
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, " ")
}

func endsSentence(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return strings.ContainsRune(".!?…:;", r)
}

// Abbreviations that end with a period but do not end a sentence.
var abbreviations = map[string]bool{
	"np": true, "itd": true, "itp": true, "tzn": true, "tj": true, "m.in": true,
	"dr": true, "prof": true, "mgr": true, "inż": true, "ul": true, "str": true, "nr": true, "godz": true,
	"e.g": true, "i.e": true, "etc": true, "vs": true, "mr": true, "mrs": true, "ms": true,
}

// Split breaks text into sentences and merges fragments shorter than minLen
// into their neighbour, so short utterances do not stutter between chunks.
func Split(text string, minLen int) []string {
	var out []string
	var cur strings.Builder
	runes := []rune(text)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		cur.WriteRune(r)
		if !strings.ContainsRune(".!?…", r) {
			continue
		}
		next := i + 1
		for next < len(runes) && strings.ContainsRune(".!?…\"')”", runes[next]) {
			next++
		}
		if next < len(runes) && !unicode.IsSpace(runes[next]) {
			continue // 3.14, file.go
		}
		if r == '.' && next == i+1 && isAbbreviation(cur.String()) {
			continue
		}
		cur.WriteString(string(runes[i+1 : next]))
		i = next - 1
		flush()
	}
	flush()

	merged := out[:0]
	for _, s := range out {
		if n := len(merged); n > 0 && utf8.RuneCountInString(merged[n-1]) < minLen {
			merged[n-1] += " " + s
			continue
		}
		merged = append(merged, s)
	}
	if n := len(merged); n > 1 && utf8.RuneCountInString(merged[n-1]) < minLen {
		merged[n-2] += " " + merged[n-1]
		merged = merged[:n-1]
	}
	return merged
}

func isAbbreviation(sentence string) bool {
	fields := strings.Fields(sentence)
	if len(fields) == 0 {
		return false
	}
	word := strings.ToLower(strings.TrimSuffix(fields[len(fields)-1], "."))
	word = strings.TrimLeft(word, "(\"'„")
	return abbreviations[word]
}

// Truncate cuts text to at most limit runes, preferring a sentence boundary.
func Truncate(text string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)[:limit]
	cut := string(runes)
	if i := strings.LastIndexAny(cut, ".!?…"); i > limit/2 {
		return cut[:i+1]
	}
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, ",;: ") + "…"
}

var polishWords = map[string]bool{
	"się": true, "jest": true, "nie": true, "czy": true, "że": true, "jak": true, "już": true,
	"to": true, "na": true, "w": true, "i": true, "z": true, "do": true, "co": true, "mam": true,
	"masz": true, "możesz": true, "skończyłem": true, "skończyłam": true, "gotowe": true, "proszę": true,
}

// DetectLang distinguishes Polish from English, the two languages the
// built-in presets cover. Other languages need an explicit lang.
func DetectLang(text string) string {
	if strings.ContainsAny(text, "ąćęłńóśźżĄĆĘŁŃÓŚŹŻ") {
		return "pl"
	}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) })
	hits := 0
	for _, w := range words {
		if polishWords[w] && w != "i" && w != "to" && w != "w" {
			hits++
		}
	}
	if hits >= 2 || (len(words) > 0 && hits*4 >= len(words)) {
		return "pl"
	}
	return "en"
}
