package service

import (
	"strings"

	"voice/internal/core"
	"voice/internal/hooks"
)

// The language a session's voice speaks: its character's when characters are
// on and it names one, else the general one (default_lang). The agent is told
// it, its announcements use it, and its speech defaults to it.

// generalLang is default_lang, English when unset.
func (s *Service) generalLang() string {
	if s.Config == nil {
		return "en"
	}
	if l := strings.ToLower(strings.TrimSpace(s.Config.DefaultLang)); l != "" {
		return l
	}
	return "en"
}

// characterLang is the language of the character called name, "" when it
// names none (or characters are off).
func (s *Service) characterLang(name string) string {
	if !s.personasOn() || name == "" {
		return ""
	}
	if ch, ok := s.Config.Character(name); ok {
		return strings.ToLower(strings.TrimSpace(ch.Lang))
	}
	return ""
}

// sessionLang is the language pid's voice speaks.
func (s *Service) sessionLang(pid uint32) string {
	if s.Agents != nil {
		if l := s.characterLang(s.Agents.Name(pid)); l != "" {
			return l
		}
	}
	return s.generalLang()
}

// speakLang is the language a call speaks in when it names none: that of the
// character it speaks as, of the session for its own voice, else the general
// one (a bare preset).
func (s *Service) speakLang(a SpeakArgs) string {
	if v := strings.TrimSpace(a.Voice); v != "" {
		if l := s.characterLang(v); l != "" {
			return l
		}
		return s.generalLang()
	}
	if a.Preset == "" {
		return s.sessionLang(a.PID)
	}
	return s.generalLang()
}

// readableIn reports whether a voice speaking lang can read text as it is:
// a Polish question is not read by an English voice ("auto" reads either).
func readableIn(text, lang string) bool {
	return lang == "" || lang == "auto" || core.DetectLang(text) == lang
}

// questionText is what is said for a question the agent asks: the question
// itself when the voice can read it, else the question phrase in its language.
func (s *Service) questionText(question, lang string) string {
	if question = strings.TrimSpace(question); question != "" && readableIn(question, lang) {
		return question
	}
	return hooks.Phrase(s.Config.Hooks, hooks.Question, s.phraseLang(lang), "")
}

// phraseLang is the language of announcement phrases for lang: with "auto"
// the user's first language.
func (s *Service) phraseLang(lang string) string {
	if lang != "auto" || s.Config == nil {
		return lang
	}
	for _, l := range s.Config.Languages {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
			return l
		}
	}
	return "en"
}

var langNames = map[string]string{"en": "English", "pl": "Polish", "de": "German", "fr": "French", "es": "Spanish",
	"it": "Italian", "pt": "Portuguese", "nl": "Dutch", "cs": "Czech", "uk": "Ukrainian", "ru": "Russian",
	"zh": "Chinese", "ja": "Japanese", "ko": "Korean"}

// LangName is a language's English name ("Polish"), or its code.
func LangName(code string) string {
	if n, ok := langNames[code]; ok {
		return n
	}
	return code
}

// SpeaksIn says in which language to speak aloud, for an instruction:
// "English", "Polish", "the language you write in".
func SpeaksIn(lang string) string {
	if lang == "auto" {
		return "the language you write in"
	}
	return LangName(lang)
}

// langNote tells the agent which language its voice speaks.
func langNote(lang string) string {
	switch lang {
	case "en":
		return "Speak aloud in English: your voice is English, even when you write in another language."
	case "auto":
		return "Speak aloud in the language you write in: your voice speaks it."
	}
	return "Speak aloud in " + LangName(lang) + ": your voice speaks " + LangName(lang) + "."
}

// takeLangNote is the note for pid's agent when it has not been told its
// voice's language yet, or that changed since (start: tell it anyway).
func (s *Service) takeLangNote(pid uint32, start bool) string {
	if s.Agents == nil || pid == 0 {
		return ""
	}
	lang := s.sessionLang(pid)
	ag := s.Agents
	ag.mu.Lock()
	defer ag.mu.Unlock()
	st := ag.seen[pid]
	if st == nil || (!start && st.toldLang == lang) {
		return ""
	}
	st.toldLang = lang
	return langNote(lang)
}
