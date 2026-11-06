package clones

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

// The user's groups of voices ("D&D", "ASMR", "Normal"): each voice names its
// group in its voice.toml; groups.toml in Dir keeps their order, so a group
// stays, in its place, while it is empty.

var groupMu sync.Mutex

type groupFile struct {
	Order []string `toml:"order"`
}

func groupsPath() string { return filepath.Join(Dir, "groups.toml") }

func readOrder() []string {
	var f groupFile
	toml.DecodeFile(groupsPath(), &f)
	return f.Order
}

func writeOrder(order []string) error {
	if Dir == "" {
		return errors.New("voice profile directory not set")
	}
	if err := os.MkdirAll(Dir, 0o755); err != nil {
		return err
	}
	var b bytes.Buffer
	b.WriteString("# The order of the cloned voices' groups; each voice names its group in its voice.toml.\n")
	if err := toml.NewEncoder(&b).Encode(groupFile{Order: order}); err != nil {
		return err
	}
	tmp := groupsPath() + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, groupsPath())
}

// GroupName checks and tidies a group's name: 1-40 characters, no line
// breaks or control characters, spaces trimmed.
func GroupName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", errors.New("give the group a name")
	}
	if utf8.RuneCountInString(name) > 40 {
		return "", fmt.Errorf("group name %q: 40 characters at most", name)
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("group name %q has control characters", name)
	}
	return name, nil
}

// find is the index of name in order, ignoring case; -1 when absent.
func find(order []string, name string) int {
	for i, g := range order {
		if strings.EqualFold(g, name) {
			return i
		}
	}
	return -1
}

// Groups is the groups in their order: the stored order, then any group a
// voice names that the order lacks (a voice.toml edited by hand).
func Groups() []string {
	groupMu.Lock()
	defer groupMu.Unlock()
	return groups(List())
}

func groups(voices []Profile) []string {
	order := readOrder()
	out := make([]string, 0, len(order))
	for _, g := range order {
		if find(out, g) < 0 {
			out = append(out, g)
		}
	}
	for _, p := range voices {
		if p.Group != "" && find(out, p.Group) < 0 {
			out = append(out, p.Group)
		}
	}
	return out
}

// AddGroup adds an empty group at the end; an existing one (any case) stays
// where it is. It returns the group's name as stored.
func AddGroup(name string) (string, error) {
	name, err := GroupName(name)
	if err != nil {
		return "", err
	}
	groupMu.Lock()
	defer groupMu.Unlock()
	return addGroup(name)
}

func addGroup(name string) (string, error) {
	order := groups(List())
	if i := find(order, name); i >= 0 {
		return order[i], nil
	}
	return name, writeOrder(append(order, name))
}

// SetGroup files a voice in group, adding the group when it is new; an empty
// group takes the voice out of any.
func SetGroup(voice, group string) (string, error) {
	groupMu.Lock()
	defer groupMu.Unlock()
	p, err := Get(voice)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(group) != "" {
		if group, err = GroupName(group); err != nil {
			return "", err
		}
		if group, err = addGroup(group); err != nil {
			return "", err
		}
	} else {
		group = ""
	}
	p.Group = group
	return group, writeMeta(p)
}

// RenameGroup renames a group and moves its voices with it; renaming to a
// group that exists merges the two.
func RenameGroup(old, name string) (string, error) {
	name, err := GroupName(name)
	if err != nil {
		return "", err
	}
	groupMu.Lock()
	defer groupMu.Unlock()
	voices := List()
	order := groups(voices)
	at := find(order, old)
	if at < 0 {
		return "", fmt.Errorf("no group %q", old)
	}
	old = order[at]
	if other := find(order, name); other >= 0 && other != at {
		name = order[other] // merge into the existing one, which keeps its place
		order = append(order[:at], order[at+1:]...)
	} else {
		order[at] = name
	}
	for _, p := range voices {
		if strings.EqualFold(p.Group, old) {
			p.Group = name
			if err := writeMeta(p); err != nil {
				return "", err
			}
		}
	}
	return name, writeOrder(order)
}

// RemoveGroup removes a group; its voices stay, in no group.
func RemoveGroup(name string) error {
	groupMu.Lock()
	defer groupMu.Unlock()
	voices := List()
	order := groups(voices)
	at := find(order, name)
	if at < 0 {
		return fmt.Errorf("no group %q", name)
	}
	for _, p := range voices {
		if strings.EqualFold(p.Group, order[at]) {
			p.Group = ""
			if err := writeMeta(p); err != nil {
				return err
			}
		}
	}
	return writeOrder(append(order[:at], order[at+1:]...))
}
