package clones

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fakeVoice stores a voice with only its voice.toml, enough for the groups.
func fakeVoice(t *testing.T, name, group string) {
	t.Helper()
	os.MkdirAll(dir(name), 0o755)
	if err := writeMeta(Profile{Name: name, Engine: "qwen3", Group: group}); err != nil {
		t.Fatal(err)
	}
}

func groupOf(t *testing.T, name string) string {
	t.Helper()
	p, err := Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return p.Group
}

func TestGroups(t *testing.T) {
	old := Dir
	Dir = t.TempDir()
	defer func() { Dir = old }()
	fakeVoice(t, "dnd-dm", "")
	fakeVoice(t, "dnd-elf", "")
	fakeVoice(t, "whisper", "")

	if g, err := AddGroup("  ASMR  "); err != nil || g != "ASMR" {
		t.Fatalf("AddGroup = %q, %v", g, err)
	}
	if g, err := SetGroup("dnd-dm", "D&D"); err != nil || g != "D&D" {
		t.Fatalf("SetGroup = %q, %v", g, err)
	}
	if g, _ := SetGroup("dnd-elf", "d&d"); g != "D&D" {
		t.Errorf("a group named in another case is the same group, got %q", g)
	}
	SetGroup("whisper", "ASMR")
	if got := Groups(); !slices.Equal(got, []string{"ASMR", "D&D"}) {
		t.Errorf("Groups = %v", got)
	}

	// An empty group stays, in its place.
	AddGroup("Normal")
	SetGroup("whisper", "")
	if got := Groups(); !slices.Equal(got, []string{"ASMR", "D&D", "Normal"}) || groupOf(t, "whisper") != "" {
		t.Errorf("after emptying ASMR: %v, whisper in %q", Groups(), groupOf(t, "whisper"))
	}

	// Renaming moves the voices; renaming onto an existing group merges.
	if _, err := RenameGroup("D&D", "Fantasy"); err != nil || groupOf(t, "dnd-dm") != "Fantasy" {
		t.Fatalf("rename: %v, dnd-dm in %q", err, groupOf(t, "dnd-dm"))
	}
	SetGroup("whisper", "ASMR")
	if g, err := RenameGroup("asmr", "fantasy"); err != nil || g != "Fantasy" || groupOf(t, "whisper") != "Fantasy" {
		t.Fatalf("merge: %q, %v, whisper in %q", g, err, groupOf(t, "whisper"))
	}
	if got := Groups(); !slices.Equal(got, []string{"Fantasy", "Normal"}) {
		t.Errorf("after merge: %v", got)
	}

	// Removing a group keeps its voices, in no group.
	if err := RemoveGroup("Fantasy"); err != nil || groupOf(t, "dnd-elf") != "" {
		t.Fatalf("remove: %v, dnd-elf in %q", err, groupOf(t, "dnd-elf"))
	}
	if got := Groups(); !slices.Equal(got, []string{"Normal"}) {
		t.Errorf("after remove: %v", got)
	}
	if err := RemoveGroup("nope"); err == nil {
		t.Error("removing a missing group: no error")
	}
	if _, err := GroupName("   "); err == nil {
		t.Error("an empty group name is accepted")
	}

	// A group a voice.toml names but the order lacks still shows.
	fakeVoice(t, "oak", "Narrators")
	if got := Groups(); !slices.Equal(got, []string{"Normal", "Narrators"}) {
		t.Errorf("hand-edited group: %v", got)
	}
	if _, err := os.Stat(filepath.Join(Dir, "groups.toml")); err != nil {
		t.Errorf("groups.toml: %v", err)
	}
}
