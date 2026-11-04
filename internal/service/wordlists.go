package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
)

// WordList is a set of words the recognizer should get right, in a JSON file:
// each word as it should be written, with how recognition writes it when it
// mishears it. A word with no forms is matched by how it sounds alone
// ("Rebecca" becomes "Rebekka").
//
//	{
//	  "id": "it-pl",
//	  "title": "IT terms in Polish speech",
//	  "about": "One sentence.",
//	  "languages": ["pl"],
//	  "default": true,
//	  "words": {
//	    "README": ["rytmi", "ridmi"],
//	    "JSON": ["dżejson"],
//	    "GitHub": []
//	  }
//	}
//
// The lists voice brings sit next to voice.exe and change with each version;
// imported ones live in the data folder and stay across updates.
type WordList struct {
	ID        string              `json:"id"`
	Title     string              `json:"title"`
	About     string              `json:"about,omitempty"`
	Languages []string            `json:"languages,omitempty"`
	Default   bool                `json:"default,omitempty"` // on until the user chooses, when it matches their languages
	Words     map[string][]string `json:"words"`
	Imported  bool                `json:"imported"` // from the data folder; the user can remove it
}

// Entries are the list in the form NewVocabulary takes: each word, and
// "heard -> word" for each form it is misheard as.
func (w WordList) Entries() []string {
	keys := make([]string, 0, len(w.Words))
	for k := range w.Words {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		// The word itself is matched by sound only when it reads like a name
		// or an identifier (README, GitHub, JSON-a): an ordinary word (build,
		// commit) would swallow others that sound like it (built, comet).
		if k != strings.ToLower(k) || strings.ContainsAny(k, "-_.'0123456789") {
			out = append(out, k)
		}
		for _, h := range w.Words[k] {
			out = append(out, h+" -> "+k)
		}
	}
	return out
}

// Count is how many words the list has.
func (w WordList) Count() int { return len(w.Words) }

var wordLists struct {
	sync.RWMutex
	shipped, imported string // folders
	all               []WordList
}

var listID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// LoadWordLists reads the lists voice brings (shipped) and the imported ones;
// an imported list replaces a shipped one with the same id.
func LoadWordLists(shipped, imported string) []error {
	var errs []error
	byID := map[string]WordList{}
	for _, dir := range []string{shipped, imported} {
		paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		for _, p := range paths {
			b, err := os.ReadFile(p)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			w, err := ParseWordList(b, strings.TrimSuffix(filepath.Base(p), ".json"))
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", p, err))
				continue
			}
			w.Imported = dir == imported
			byID[w.ID] = w
		}
	}
	all := make([]WordList, 0, len(byID))
	for _, w := range byID {
		all = append(all, w)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Imported != all[j].Imported {
			return !all[i].Imported // voice's own first
		}
		return all[i].Title < all[j].Title
	})
	wordLists.Lock()
	wordLists.shipped, wordLists.imported, wordLists.all = shipped, imported, all
	wordLists.Unlock()
	return errs
}

// ParseWordList reads and checks a list; id names it when the file does not.
func ParseWordList(b []byte, id string) (WordList, error) {
	var w WordList
	b = []byte(strings.TrimPrefix(string(b), "\ufeff"))
	if err := json.Unmarshal(b, &w); err != nil {
		return WordList{}, fmt.Errorf("not a word list: %w", err)
	}
	if w.ID == "" {
		w.ID = strings.ToLower(id)
	}
	if !listID.MatchString(w.ID) {
		return WordList{}, fmt.Errorf("id %q: use lowercase letters, digits, - or _", w.ID)
	}
	if w.Title == "" {
		w.Title = w.ID
	}
	words := map[string][]string{}
	for k, forms := range w.Words {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		clean := []string{}
		for _, f := range forms {
			if f = strings.TrimSpace(f); f != "" && !strings.EqualFold(f, k) && !slices.Contains(clean, f) {
				clean = append(clean, f)
			}
		}
		words[k] = clean
	}
	if len(words) == 0 {
		return WordList{}, errors.New(`the list has no words ("words": {"README": ["rytmi"]})`)
	}
	w.Words = words
	return w, nil
}

// WordLists returns every list, voice's own first.
func WordLists() []WordList {
	wordLists.RLock()
	defer wordLists.RUnlock()
	return append([]WordList(nil), wordLists.all...)
}

// ImportWordList stores a list in the data folder and loads it.
func ImportWordList(b []byte, name string) (WordList, error) {
	w, err := ParseWordList(b, strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)))
	if err != nil {
		return WordList{}, err
	}
	wordLists.RLock()
	shipped, imported := wordLists.shipped, wordLists.imported
	wordLists.RUnlock()
	if imported == "" {
		return WordList{}, errors.New("word lists are not set up")
	}
	if err := os.MkdirAll(imported, 0o755); err != nil {
		return WordList{}, err
	}
	out, _ := json.MarshalIndent(w, "", "  ")
	if err := os.WriteFile(filepath.Join(imported, w.ID+".json"), out, 0o644); err != nil {
		return WordList{}, err
	}
	LoadWordLists(shipped, imported)
	w.Imported = true
	return w, nil
}

// RemoveWordList deletes an imported list; voice's own cannot be removed,
// only turned off.
func RemoveWordList(id string) error {
	wordLists.RLock()
	shipped, imported := wordLists.shipped, wordLists.imported
	wordLists.RUnlock()
	if !listID.MatchString(id) {
		return fmt.Errorf("no word list %q", id)
	}
	if err := os.Remove(filepath.Join(imported, id+".json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%q is not an imported list; turn it off instead", id)
		}
		return err
	}
	LoadWordLists(shipped, imported)
	return nil
}

// ListsOn are the lists in use: the ones the user turned on, or, until they
// choose (on is nil), the default ones in a language they speak.
func ListsOn(on []string, languages []string) []WordList {
	var out []WordList
	for _, w := range WordLists() {
		if on != nil {
			if slices.Contains(on, w.ID) {
				out = append(out, w)
			}
			continue
		}
		if w.Default && (len(w.Languages) == 0 || len(languages) == 0 || slices.ContainsFunc(w.Languages, func(l string) bool { return slices.Contains(languages, l) })) {
			out = append(out, w)
		}
	}
	return out
}
