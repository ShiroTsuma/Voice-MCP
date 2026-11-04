package settingsui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"voice/internal/preset"
)

func TestPageAndSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	mux := http.NewServeMux()
	Register(mux, Deps{
		Token: "tok", SettingsPath: path,
		State:   func() map[string]any { return map[string]any{} },
		Reload:  func() (bool, error) { return false, nil },
		Status:  func() map[string]any { return map[string]any{"muted": false} },
		Control: func(string) error { return nil },
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	res, err := client.Get(srv.URL + "/settings")
	if err != nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("without token: %v %v", res.StatusCode, err)
	}
	res, err = client.Get(srv.URL + "/settings?t=tok")
	if err != nil || res.StatusCode != http.StatusSeeOther || len(res.Cookies()) != 1 {
		t.Fatalf("token login: %v %v", res.StatusCode, err)
	}
	cookie := res.Cookies()[0]

	req, _ := http.NewRequest("GET", srv.URL+"/settings", nil)
	req.AddCookie(cookie)
	res, err = client.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("page: %v %v", res.StatusCode, err)
	}

	body := `{"volume":0.5,"serve":{"dictate_feedback":"both","stop_hotkey":"Ctrl+Shift+K"}}`
	req, _ = http.NewRequest("POST", srv.URL+"/settings/api/save", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	if res, err = client.Do(req); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("save: %v %v", res.StatusCode, err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"volume = 0.5", `dictate_feedback = "both"`, `stop_hotkey = "Ctrl+Shift+K"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("settings.toml lacks %s:\n%s", want, b)
		}
	}
}

func TestTunedPresetRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	mux := http.NewServeMux()
	Register(mux, Deps{
		Token: "tok", SettingsPath: path,
		State:  func() map[string]any { return map[string]any{} },
		Reload: func() (bool, error) { return false, nil },
	})
	save := func(body string) {
		req := httptest.NewRequest("POST", "/settings/api/save", strings.NewReader(body))
		req.Header.Set("X-Voice-Token", "tok")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("save: %d %s", rec.Code, rec.Body)
		}
	}
	save(`{"presets":{"voice-rebekka":{"extends":"calm-mature","speed":1.1,"pitch":-1,"dsp":["reverb:hall"]},"calm-mature":{"extends":"x"},"voice-preview":{"extends":"x"}}}`)

	cfg, err := preset.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := cfg.Resolve("voice-rebekka")
	if err != nil || r.Speed != 1.1 || r.Pitch != -1 || len(r.DSP) != 1 || r.DSP[0] != "reverb:hall" || r.Engine == "" {
		t.Fatalf("tuned preset: %+v %v", r, err)
	}
	if cfg.PresetExtends("voice-rebekka") != "calm-mature" {
		t.Fatal("extends lost")
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "voice-preview") || strings.Contains(string(b), "[presets.calm-mature]") {
		t.Fatalf("only tuned voices may be written:\n%s", b)
	}

	save(`{"presets":{"voice-rebekka":null}}`)
	if cfg, _ := preset.Load(path); cfg.HasPreset("voice-rebekka") {
		t.Fatal("a null preset was not removed")
	}
}
