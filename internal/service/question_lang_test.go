package service

import (
	"testing"

	"voice/internal/hooks"
	"voice/internal/preset"
)

// A question the voice cannot read in its language is announced with the
// question phrase instead of being read as it is.
func TestQuestionTextKeepsToTheVoicesLanguage(t *testing.T) {
	s := &Service{Config: &preset.Config{}}
	pl := "Które sklonowane głosy usunąć? Warianty lunamist, Głosy D&D, or something else."
	en := "Which voices should go? lunamist takes, D&D voices, or something else."
	phrase := hooks.Phrase(s.Config.Hooks, hooks.Question, "en", "")
	if phrase == "" {
		t.Fatal("no English question phrase")
	}
	if got := s.questionText(pl, "en"); got != phrase {
		t.Errorf("Polish question, English voice: %q, want the phrase %q", got, phrase)
	}
	if got := s.questionText(en, "en"); got != en {
		t.Errorf("English question, English voice: %q", got)
	}
	if got := s.questionText(pl, "pl"); got != pl {
		t.Errorf("Polish question, Polish voice: %q", got)
	}
	if got := s.questionText(pl, "auto"); got != pl {
		t.Errorf("Polish question, auto voice: %q", got)
	}
}
