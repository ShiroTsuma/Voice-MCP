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
		{"uruchom testy, Codex.", "uruchom testy", []uint32{2}, false},
		{"Wszyscy: kończymy", "Kończymy", []uint32{1, 2, 3}, true},
		{"do wszystkich, kończymy", "Kończymy", []uint32{1, 2, 3}, true},
		{"Popraw błąd w parserze", "Popraw błąd w parserze", nil, false},
		{"Codex", "Codex", nil, false}, // only a name: not an address
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
