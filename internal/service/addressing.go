package service

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The words Route reads addresses by come from JSON files, one per language
// (addressing/en.json, pl.json): the ones built in, and any the user puts in
// <data>\addressing, which add words or languages. Words are compared
// normalized (no case, no diacritics), so the files spell them naturally.

//go:embed addressing/*.json
var builtinAddressing embed.FS

// addressingFile is one language's file.
type addressingFile struct {
	Everyone      []string            `json:"everyone"`       // address every session ("everyone", "do wszystkich")
	Openers       []string            `json:"openers"`        // short replies a message may start with before the name ("Sure, Clara. ...")
	YouWords      []string            `json:"you_words"`      // words shortly before a name that speak to someone ("do you think Anna")
	AboutBefore   []string            `json:"about_before"`   // right before a name: it is the object ("tell Rebekka")
	AboutAfter    []string            `json:"about_after"`    // right after a name: it is the subject ("Anna said")
	SubjectVerbs  []string            `json:"subject_verbs"`  // right after a name at the start: the sentence is about it
	ClientAliases map[string][]string `json:"client_aliases"` // how a recognizer writes a client's name
}

type wordSet map[string]bool

var (
	addrMu        sync.RWMutex
	everyone      [][]string
	openers       = wordSet{}
	youWords      = wordSet{}
	aboutBefore   = wordSet{}
	aboutAfter    = wordSet{}
	subjectVerb   = wordSet{}
	clientAliases = map[string][]string{}
)

func init() {
	if errs := LoadAddressing(""); len(errs) > 0 {
		panic(fmt.Sprint("built-in addressing words: ", errs))
	}
}

// LoadAddressing reads the built-in files and then those in dir (none when
// empty or missing); a broken file of the user's is skipped and reported.
func LoadAddressing(dir string) []error {
	var files []addressingFile
	var errs []error
	read := func(fsys fs.FS, name string) {
		b, err := fs.ReadFile(fsys, name)
		if err == nil {
			var f addressingFile
			if err = json.Unmarshal(b, &f); err == nil {
				files = append(files, f)
				return
			}
		}
		errs = append(errs, fmt.Errorf("%s: %w", name, err))
	}
	names, _ := fs.Glob(builtinAddressing, "addressing/*.json")
	for _, n := range names {
		read(builtinAddressing, n)
	}
	if dir != "" {
		user, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		for _, p := range user {
			read(os.DirFS(filepath.Dir(p)), filepath.Base(p))
		}
	}

	set := func(lists func(addressingFile) []string) wordSet {
		s := wordSet{}
		for _, f := range files {
			for _, w := range lists(f) {
				if n := normalize(w); n != "" {
					s[n] = true
				}
			}
		}
		return s
	}
	var all [][]string
	aliases := map[string][]string{}
	for _, f := range files {
		for _, phrase := range f.Everyone {
			var ws []string
			for _, w := range strings.Fields(phrase) {
				ws = append(ws, normalize(w))
			}
			if len(ws) > 0 {
				all = append(all, ws)
			}
		}
		for client, names := range f.ClientAliases {
			for _, n := range names {
				aliases[client] = append(aliases[client], normalize(n))
			}
		}
	}

	addrMu.Lock()
	defer addrMu.Unlock()
	everyone = all
	openers = set(func(f addressingFile) []string { return f.Openers })
	youWords = set(func(f addressingFile) []string { return f.YouWords })
	aboutBefore = set(func(f addressingFile) []string { return f.AboutBefore })
	aboutAfter = set(func(f addressingFile) []string { return f.AboutAfter })
	subjectVerb = set(func(f addressingFile) []string { return f.SubjectVerbs })
	clientAliases = aliases
	return errs
}
