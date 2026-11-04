package settingsui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
