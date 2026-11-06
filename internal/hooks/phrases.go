package hooks

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"voice/internal/preset"
)

// The phrases announcements use, one JSON file per language (phrases/en.json,
// pl.json): the ones built in, and any the user puts in <data>\phrases, which
// add a language or replace a list of one.

//go:embed phrases/*.json
var builtinPhrases embed.FS

// Kinds of phrases beyond the events: a permission prompt that names its tool
// ({tool}), and Claude Code's question dialog.
const (
	PermissionTool = "permission_tool"
	Question       = "question"
)

var (
	phrasesMu sync.RWMutex
	phrases   = map[string]map[string][]string{} // language -> kind -> phrases
)

func init() {
	if errs := LoadPhrases(""); len(errs) > 0 {
		panic(fmt.Sprint("built-in phrases: ", errs))
	}
}

// LoadPhrases reads the built-in files and then those in dir (none when empty
// or missing); a broken file of the user's is skipped and reported.
func LoadPhrases(dir string) []error {
	all := map[string]map[string][]string{}
	var errs []error
	read := func(fsys fs.FS, name string) {
		b, err := fs.ReadFile(fsys, name)
		var f map[string]json.RawMessage
		if err == nil {
			err = json.Unmarshal(b, &f)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			return
		}
		lang := strings.ToLower(strings.TrimSuffix(filepath.Base(name), ".json"))
		if all[lang] == nil {
			all[lang] = map[string][]string{}
		}
		for kind, raw := range f {
			var list []string
			if json.Unmarshal(raw, &list) == nil && len(list) > 0 {
				all[lang][kind] = list
			}
		}
	}
	names, _ := fs.Glob(builtinPhrases, "phrases/*.json")
	for _, n := range names {
		read(builtinPhrases, n)
	}
	if dir != "" {
		user, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		for _, p := range user {
			read(os.DirFS(filepath.Dir(p)), filepath.Base(p))
		}
	}
	phrasesMu.Lock()
	phrases = all
	phrasesMu.Unlock()
	return errs
}

// Phrase picks a phrase for kind at random, so repeated notifications do not
// sound canned: from [hooks.phrases] in the config when it has the kind (in
// any language), else in lang, else in English. {tool} becomes tool.
func Phrase(h preset.Hooks, kind, lang, tool string) string {
	list := h.Phrases[kind]
	if len(list) == 0 {
		phrasesMu.RLock()
		list = phrases[strings.ToLower(lang)][kind]
		if len(list) == 0 {
			list = phrases["en"][kind]
		}
		phrasesMu.RUnlock()
	}
	if len(list) == 0 {
		return ""
	}
	return strings.ReplaceAll(list[rand.IntN(len(list))], "{tool}", tool)
}
