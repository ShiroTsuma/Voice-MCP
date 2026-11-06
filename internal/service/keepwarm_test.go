package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"voice/internal/core"
	"voice/internal/preset"
)

// keep_warm is opt-in on register, through the Session API and the MCP's
// voice_control alike, and says what it does on this PC: the built-in voices
// run on the CPU, so there is nothing to keep loaded and the session is not
// kept on the list.
func TestRegisterKeepWarm(t *testing.T) {
	cfg, err := preset.Load("")
	if err != nil {
		t.Fatal(err)
	}
	sp := core.NewSpeaker(cfg)
	sp.RecordTo(filepath.Join(t.TempDir(), "out.wav"))
	t.Cleanup(sp.Close)
	s := &Service{Speaker: sp, Config: cfg, Agents: &Agents{}}
	ctx := context.Background()

	res, err := s.Session(ctx, SessionArgs{Action: "register", Session: "a", PID: 21, Name: "Mira"})
	if err != nil || res.(SessionResult).Warm != "" {
		t.Fatalf("without keep_warm: %+v, %v", res, err)
	}
	res, err = s.Session(ctx, SessionArgs{Action: "register", Session: "a", PID: 21, KeepWarm: true})
	if err != nil {
		t.Fatal(err)
	}
	if w := res.(SessionResult).Warm; !strings.Contains(w, "runs on the CPU") {
		t.Fatalf("a CPU voice kept warm: %q", w)
	}
	if len(s.Agents.KeepWarm()) != 0 {
		t.Fatal("a session with nothing to keep is on the keep-warm list")
	}

	out, err := s.Control(ctx, ControlArgs{Action: "register", PID: 22, Client: "claude", KeepWarm: true})
	if err != nil || !strings.Contains(out.(string), "keep_warm:") {
		t.Fatalf("MCP register: %v, %v", out, err)
	}
	s.keepWarmTick() // with nobody on the list: nothing happens

	s.Agents.SetKeepWarm(21, true)
	if got := s.Agents.KeepWarm(); len(got) != 1 || got[0] != 21 {
		t.Fatalf("keep-warm list %v", got)
	}
	s.keepWarmTick() // touches the CPU voice: no server, nothing to do
	s.Agents.Forget(21)
	if len(s.Agents.KeepWarm()) != 0 {
		t.Fatal("an ended session is still kept warm")
	}
}

// A voice on a speech server voice does not start (Higgs over the OpenAI
// speech API) is not voice's to keep, and the answer says so instead of
// calling it quick to load.
func TestKeepWarmOutsideServer(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "config.toml")
	os.WriteFile(conf, []byte("default_preset = \"far\"\n[engines.far]\ntype = \"openai\"\nurl = \"http://127.0.0.1:1\"\n[presets.far]\nengine = \"far\"\nchime = \"none\"\n"), 0o644)
	cfg, err := preset.Load(conf)
	if err != nil {
		t.Fatal(err)
	}
	sp := core.NewSpeaker(cfg)
	t.Cleanup(sp.Close)
	s := &Service{Speaker: sp, Config: cfg, Agents: &Agents{}}
	res, err := s.Session(context.Background(), SessionArgs{Action: "register", Session: "h", PID: 31, KeepWarm: true})
	if err != nil {
		t.Fatal(err)
	}
	if w := res.(SessionResult).Warm; !strings.Contains(w, "does not start") {
		t.Fatalf("outside server: %q", w)
	}
}
