package main

import (
	"os"
	"path/filepath"
	"testing"

	"voice/internal/engine/httpengine"
	"voice/internal/integrations"
	"voice/sidecar"
)

func TestIntegrationFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	os.WriteFile(filepath.Join(dir, "here.txt"), []byte("x"), 0o644)
	handlers := map[string]handler{"builtin": {status: func(integrationFile) integrations.Status {
		return integrations.Status{State: integrations.On, Detail: "Built in"}
	}}}

	// A file with its own check and commands.
	f := readIntegrationFile(write("mine.toml", `
title = "Mine"
group = "speech"
[check]
path = "here.txt"
[[actions]]
id = "remove"
when = "installed"
command = ["cmd", "/c", "echo removed"]
[[actions]]
id = "install"
when = "missing"
command = ["cmd", "/c", "echo installed"]
`))
	if f.ID != "mine" || f.Group != "speech" {
		t.Fatalf("defaults: %+v", f)
	}
	it := makeIntegration(f, handlers)
	st := it.Status()
	if st.State != integrations.On || len(st.Actions) != 1 || st.Actions[0].ID != "remove" {
		t.Fatalf("installed file integration: %+v", st)
	}
	r := &integrations.Registry{}
	r.Add(it)
	job, err := r.Start("mine", "remove")
	if err != nil || !job.Done || job.Error != "" || len(job.Log) < 2 || job.Log[1] != "removed" {
		t.Fatalf("command action: %+v %v", job, err)
	}

	// A handler, and the problems a file can have.
	if st := makeIntegration(readIntegrationFile(write("sapi.toml", `uses = "builtin"`)), handlers).Status(); st.State != integrations.On {
		t.Fatalf("handler: %+v", st)
	}
	if st := makeIntegration(readIntegrationFile(write("odd.toml", `uses = "teleport"`)), handlers).Status(); st.State != integrations.Error {
		t.Fatalf("unknown handler: %+v", st)
	}
	if st := makeIntegration(readIntegrationFile(write("bad.toml", `title = "unclosed`)), handlers).Status(); st.State != integrations.Error {
		t.Fatalf("broken file: %+v", st)
	}
}

func TestShippedIntegrationFilesParse(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join("..", "..", "integrations", "*.toml"))
	if len(paths) == 0 {
		t.Skip("no shipped integration files here")
	}
	known := builtinHandlers(nil)
	for _, p := range paths {
		f := readIntegrationFile(p)
		if f.Uses == "broken" {
			t.Errorf("%s: %s", p, f.About)
		}
		if _, ok := known[f.Uses]; f.Uses != "" && !ok {
			t.Errorf("%s: unknown uses %q", p, f.Uses)
		}
	}
}

func TestSidecarStatusReadsBOM(t *testing.T) {
	old := httpengine.SidecarDir
	defer func() { httpengine.SidecarDir = old }()
	httpengine.SidecarDir = t.TempDir()
	env := filepath.Join(httpengine.SidecarDir, "envs", "chatterbox")
	os.MkdirAll(env, 0o755)
	os.WriteFile(filepath.Join(env, ".voice-setup.json"), []byte("\xEF\xBB\xBF{\"gpu\": \"nvidia\", \"backend\": true}"), 0o644)
	if st := sidecarStatus("chatterbox"); st.State != integrations.On {
		t.Fatalf("%+v", st)
	}
}

// Chatterbox and Turbo share one environment: removing either keeps it for
// the other; removing the last one removes it.
func TestRemoveSharedEnvironment(t *testing.T) {
	old := httpengine.SidecarDir
	defer func() { httpengine.SidecarDir = old }()
	httpengine.SidecarDir = t.TempDir()
	env := filepath.Join(httpengine.SidecarDir, "envs", "chatterbox")
	os.MkdirAll(env, 0o755)
	os.WriteFile(filepath.Join(env, ".voice-setup.json"), []byte(`{"gpu": "nvidia", "backend": true, "backend:chatterbox_turbo": true}`), 0o644)
	cb, _ := sidecar.Find("chatterbox")
	turbo, _ := sidecar.Find("chatterbox_turbo")
	quiet := func(string) {}

	if err := removeShared(cb, quiet); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(env); err != nil {
		t.Fatal("the environment went with Chatterbox while Turbo uses it")
	}
	if st := sidecarStatus("chatterbox"); st.State != integrations.Missing {
		t.Fatalf("chatterbox: %+v", st)
	}
	if st := sidecarStatus("chatterbox_turbo"); st.State != integrations.On {
		t.Fatalf("turbo: %+v", st)
	}
	if err := removeShared(turbo, quiet); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(env); !os.IsNotExist(err) {
		t.Fatal("the environment stayed after its last backend")
	}
	if st := sidecarStatus("chatterbox_turbo"); st.State != integrations.Missing {
		t.Fatalf("turbo after: %+v", st)
	}
}

// Start and stop run on this PC, so only a server on it gets them.
func TestLocalAddress(t *testing.T) {
	for addr, want := range map[string]bool{
		"http://127.0.0.1:8000": true, "http://localhost:8000": true, "http://[::1]:8000": true,
		"http://192.168.1.20:8000": false, "https://gpu.example.com": false, "not a url": false,
	} {
		if got := localAddress(addr); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
}
