package service

import (
	"slices"
	"testing"
)

func TestRoute(t *testing.T) {
	agents := []AgentInfo{
		{PID: 1, Name: "Annie", Client: "claude-code", Project: "MCP-Tools"},
		{PID: 2, Client: "codex", Project: "shop"},
		{PID: 3, Name: "Rebekka", Client: "claude", Project: "shop"},
	}
	cases := []struct {
		text, rest string
		to         []uint32
		all        bool
	}{
		{"Codex, uruchom testy", "Uruchom testy", []uint32{2}, false},
		{"kodeks uruchom testy", "Uruchom testy", []uint32{2}, false},
		{"Claude: commit", "Commit", []uint32{1}, false}, // the most recent Claude
		{"Claude shop, zrób deploy", "Zrób deploy", []uint32{3}, false},
		{"mcp tools: dodaj test", "Dodaj test", []uint32{1}, false},
		{"MCP tool, dodaj test", "Dodaj test", []uint32{1}, false}, // singular for plural
		{"shop sprawdź logi", "Sprawdź logi", []uint32{2}, false},  // the most recent agent in shop
		{"Rebekka, zrób deploy", "Zrób deploy", []uint32{3}, false},
		{"Rebeka zrób deploy", "Zrób deploy", []uint32{3}, false}, // one letter off
		{"Annie: commit", "Commit", []uint32{1}, false},
		{"can you say something nice Rebekka", "can you say something nice", []uint32{3}, false}, // the name last
		{"You can make it appear, Rebecca.", "You can make it appear", []uint32{3}, false},       // spelled as it sounds
		{"Rebeca, zrób deploy", "Zrób deploy", []uint32{3}, false},
		{"Kodex, uruchom testy", "Uruchom testy", []uint32{2}, false},
		{"Zrób deploy, Rebekka?", "Zrób deploy?", []uint32{3}, false},
		{"uruchom testy, Codex.", "uruchom testy, Codex.", nil, false},                                     // a client said last is content
		{"to działa tylko z Claude'em i do Codexa", "to działa tylko z Claude'em i do Codexa", nil, false}, // as in a real dictation
		{"Wszyscy: kończymy", "Kończymy", []uint32{1, 2, 3}, true},
		{"do wszystkich, kończymy", "Kończymy", []uint32{1, 2, 3}, true},
		{"Popraw błąd w parserze", "Popraw błąd w parserze", nil, false},
		{"Codex", "Codex", nil, false}, // only a name: not an address

		// A session's name inside the sentence.
		{"Do you think Annie that we should do X or Y?", "Do you think Annie that we should do X or Y?", []uint32{1}, false},
		{"So what do you think, Rebekka, should we ship it?", "So what do you think, Rebekka, should we ship it?", []uint32{3}, false},
		{"I wonder, Rebecca, if this is done.", "I wonder, Rebecca, if this is done.", []uint32{3}, false},
		{"Czy myślisz Rebekka, że to zadziała?", "Czy myślisz Rebekka, że to zadziała?", []uint32{3}, false},
		{"A ty Rebekko co o tym sądzisz?", "A ty Rebekko co o tym sądzisz?", []uint32{3}, false}, // vocative
		{"Słuchaj Rebeko, zrób deploy jutro", "Słuchaj Rebeko, zrób deploy jutro", []uint32{3}, false},
		{"Please tell Rebekka that the build is green", "Please tell Rebekka that the build is green", nil, false},
		{"Yesterday, Rebekka fixed the parser", "Yesterday, Rebekka fixed the parser", nil, false},
		{"I think Rebekka said it was done", "I think Rebekka said it was done", nil, false},
		{"zapytaj Rebekkę o logi", "zapytaj Rebekkę o logi", nil, false},
		{"pracowałem z Rebekką nad tym", "pracowałem z Rebekką nad tym", nil, false},
		{"Rebekka's change broke the tests", "Rebekka's change broke the tests", nil, false},
		{"Is the change of Rebekka's done", "Is the change of Rebekka's done", nil, false},
		{"Do you think Annie, that Rebekka was right?", "Do you think Annie, that Rebekka was right?", []uint32{1}, false},
		{"What do you think Annie, should Rebekka, do it?", "What do you think Annie, should Rebekka, do it?", nil, false}, // two names called
		{"So what do you think Rebecca? Should we ship it today?", "So what do you think Rebecca? Should we ship it today?", []uint32{3}, false},
		// After a short reply.
		{"Sure, Rebekka. Remove them.", "Sure, Rebekka. Remove them.", []uint32{3}, false},
		{"Okay Annie do it", "Okay Annie do it", []uint32{1}, false},
		{"Dobra Rebeko, wdrażaj", "Dobra Rebeko, wdrażaj", []uint32{3}, false},
		{"Yes, Rebekka said so", "Yes, Rebekka said so", nil, false}, // about her
		{"Sure, tell Rebekka", "Sure, tell", []uint32{3}, false},     // the name last still addresses

		// Found by speaking test sentences to the recognizers.
		{"Rebekko, zrób deploy", "Zrób deploy", []uint32{3}, false}, // the vocative first
		{"Zrób deploy, Rebekko.", "Zrób deploy", []uint32{3}, false},
		{"Rebekka powiedziała, że to już działa.", "Rebekka powiedziała, że to już działa.", nil, false},
		{"Rebecca said it works", "Rebecca said it works", nil, false},
		{"Rebeka zrób deploy jutro", "Zrób deploy jutro", []uint32{3}, false},
	}
	for _, c := range cases {
		to, rest, all := Route(c.text, agents)
		if !slices.Equal(to, c.to) || rest != c.rest || all != c.all {
			t.Errorf("%q: got %v %q %v, want %v %q %v", c.text, to, rest, all, c.to, c.rest, c.all)
		}
	}
}

func TestProjectWords(t *testing.T) {
	for in, want := range map[string][]string{"MCP-Tools": {"mcp", "tools"}, "voiceAssistant": {"voice", "assistant"}, "Gra_Zręcznościowa": {"gra", "zrecznosciowa"}} {
		if got := projectWords(in); !slices.Equal(got, want) {
			t.Errorf("%s: %v", in, got)
		}
	}
}
