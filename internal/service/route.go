package service

import (
	"strings"
	"time"
	"unicode"
)

// AgentInfo names an agent for routing and for the list of recipients.
type AgentInfo struct {
	PID      uint32    `json:"pid"`
	Name     string    `json:"name"`      // what the user calls it: Annie, Rebekka, ...
	Client   string    `json:"client"`    // claude, codex (from hooks or the MCP client's name)
	Project  string    `json:"project"`   // working directory name
	Busy     bool      `json:"busy"`      // in the middle of a turn
	Waiting  int       `json:"waiting"`   // dictated messages it has not taken yet
	Active   time.Time `json:"active"`    // last activity
	NoInput  bool      `json:"no_input"`  // excluded from dictation by the user
	NoSpeech bool      `json:"no_speech"` // muted by the user
}

// How a recognizer writes the clients' names, in English and Polish.
var clientAliases = map[string][]string{
	"claude": {"claude", "klaud", "klod", "klaude", "cloud", "clod", "clode"},
	"codex":  {"codex", "kodeks", "kodex", "codeks", "kodek", "codecs"},
}

// Words that address every agent.
var everyone = [][]string{{"wszyscy"}, {"do", "wszystkich"}, {"wszystkim"}, {"everyone"}, {"everybody"}, {"all", "agents"}, {"to", "all"}}

// Route reads who a dictated message is for from its first words: a client
// ("Codex, ..."), a project ("mcp tools: ..."), both ("Claude mcp tools ..."),
// or everyone ("wszyscy: ..."). It returns the agents addressed and the text
// without the address; no address returns nothing and the text as it is.
// agents are ordered most recently active first; among several that match,
// the first wins.
func Route(text string, agents []AgentInfo) (to []uint32, rest string, all bool) {
	words := strings.Fields(text)
	norm := make([]string, len(words))
	for i, w := range words {
		norm[i] = normalize(w)
	}
	skip := func(n int) string {
		r := strings.Join(words[n:], " ")
		r = strings.TrimLeft(r, " ,:;-–—.!")
		if r != "" {
			runes := []rune(r)
			runes[0] = unicode.ToUpper(runes[0])
			r = string(runes)
		}
		return r
	}
	for _, e := range everyone {
		if n := matchWords(norm, e); n > 0 && len(words) > n {
			for _, a := range agents {
				to = append(to, a.PID)
			}
			return to, skip(n), true
		}
	}
	// Longest address first: "claude mcp tools" before "claude".
	bestN, bestScore := 0, 0
	var best uint32
	for _, a := range agents {
		client := clientKind(a.Client)
		for i, addr := range addresses(a.Name, client, a.Project) {
			n := matchWords(norm, addr)
			if n == 0 || n >= len(words) {
				continue
			}
			score := n*2 + len(addr) // more words, then more specific names
			if i == 0 && a.Name != "" {
				score += 100 // a session's own name beats its client or project
			}
			if score > bestScore {
				best, bestN, bestScore = a.PID, n, score
			}
		}
	}
	if bestN > 0 {
		return []uint32{best}, skip(bestN), false
	}
	// "..., Rebekka": a session's name said last addresses too. A client or
	// project said last does not: it ends a sentence as often as it names
	// someone ("... works with Claude and Codex").
	bestScore = 0
	for _, a := range agents {
		name := normalize(a.Name)
		if name == "" || len(words) < 2 || matchWords(norm[len(norm)-1:], []string{name}) == 0 {
			continue
		}
		if score := len(name); score > bestScore {
			best, bestN, bestScore = a.PID, 1, score
		}
	}
	if bestN == 0 {
		return nil, text, false
	}
	rest = strings.TrimRight(strings.Join(words[:len(words)-bestN], " "), " ,:;-–—")
	if strings.HasSuffix(strings.TrimSpace(text), "?") && !strings.HasSuffix(rest, "?") {
		rest += "?" // the question mark came after the name
	}
	return []uint32{best}, rest, false
}

// addresses are the word sequences that name an agent.
func addresses(name, client, project string) [][]string {
	var out [][]string
	if n := normalize(name); n != "" {
		out = append(out, []string{n})
	}
	proj := projectWords(project)
	for _, c := range clientAliases[client] {
		out = append(out, []string{c})
		if len(proj) > 0 {
			out = append(out, append([]string{c}, proj...))
		}
	}
	if len(proj) > 0 {
		out = append(out, proj)
		if len(proj) > 1 {
			out = append(out, []string{strings.Join(proj, "")}) // "mcptools"
		}
	}
	return out
}

// matchWords reports how many leading words of norm spell addr, letting a
// recognizer's small mistakes through; 0 when they do not.
func matchWords(norm, addr []string) int {
	if len(addr) == 0 || len(norm) < len(addr) {
		return 0
	}
	for i, a := range addr {
		if !close(norm[i], a) {
			return 0
		}
	}
	return len(addr)
}

// close compares two normalized words, allowing one edit in words of five
// letters or more and two in words of eight or more.
func close(a, b string) bool {
	if a == b || a+"s" == b || b+"s" == a { // "tool" for "tools"
		return true
	}
	// A recognizer spells a name the way it sounds: "Rebecca" for "Rebekka".
	a, b = sound(a), sound(b)
	if a == b || a+"s" == b || b+"s" == a {
		return true
	}
	allowed := 0
	switch n := min(len([]rune(a)), len([]rune(b))); {
	case n >= 8:
		allowed = 2
	case n >= 5:
		allowed = 1
	}
	return allowed > 0 && distance(a, b) <= allowed
}

var soundAlike = strings.NewReplacer("ph", "f", "ck", "k", "c", "k", "q", "k", "x", "ks", "w", "v", "y", "i")

// sound reduces a normalized word to how it sounds: letters that sound alike
// become one, and doubled letters count once ("rebecca" and "rebekka" are
// both "rebeka", "codex" and "kodeks" both "kodeks").
func sound(w string) string {
	w = soundAlike.Replace(w)
	var b strings.Builder
	var prev rune
	for _, r := range w {
		if r != prev {
			b.WriteRune(r)
		}
		prev = r
	}
	return b.String()
}

func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func clientKind(c string) string {
	c = strings.ToLower(c)
	switch {
	case strings.Contains(c, "claude"):
		return "claude"
	case strings.Contains(c, "codex"):
		return "codex"
	}
	return ""
}

// projectWords splits a folder name into spoken words: "MCP-Tools" is
// "mcp tools", "voiceAssistant" is "voice assistant".
func projectWords(p string) []string {
	var b strings.Builder
	prev := rune(0)
	for _, r := range p {
		if unicode.IsUpper(r) && unicode.IsLower(prev) {
			b.WriteRune(' ')
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
		prev = r
	}
	var out []string
	for _, w := range strings.Fields(b.String()) {
		out = append(out, normalize(w))
	}
	return out
}

var plain = strings.NewReplacer("ą", "a", "ć", "c", "ę", "e", "ł", "l", "ń", "n", "ó", "o", "ś", "s", "ź", "z", "ż", "z")

// normalize lower-cases a word and drops Polish diacritics and punctuation.
func normalize(w string) string {
	w = plain.Replace(strings.ToLower(w))
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, w)
}
