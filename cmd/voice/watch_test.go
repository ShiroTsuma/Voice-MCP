package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchFilesSeesWritesCreatesAndRemoves(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "config.toml"), filepath.Join(dir, "settings.toml")
	os.WriteFile(a, []byte("x = 1\n"), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes := make(chan struct{}, 8)
	go watchFiles(ctx, 10*time.Millisecond, []string{a, b}, func() { changes <- struct{}{} })

	expect := func(what string) {
		t.Helper()
		select {
		case <-changes:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s was not seen", what)
		}
		// One write can be seen in two steps (the file created empty, then
		// filled), each a change of its own; take them until it is quiet.
		for {
			select {
			case <-changes:
			case <-time.After(40 * time.Millisecond):
				return
			}
		}
	}
	quiet := func() {
		t.Helper()
		select {
		case <-changes:
			t.Fatal("a change was reported with nothing changed")
		case <-time.After(60 * time.Millisecond):
		}
	}

	quiet()
	os.WriteFile(b, []byte("default_preset = \"neutral\"\n"), 0o644)
	expect("a created file")
	quiet()
	os.WriteFile(a, []byte("x = 22\n"), 0o644) // the size changes even within one clock tick
	expect("a written file")
	os.Remove(b)
	expect("a removed file")
}
