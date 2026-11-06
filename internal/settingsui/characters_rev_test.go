package settingsui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A characters window that loaded the list before someone else changed it
// must not save its old copy over theirs.
func TestStaleCharactersAreNotSaved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	mux := http.NewServeMux()
	Register(mux, Deps{
		Token: "tok", SettingsPath: path,
		State:  func() map[string]any { return map[string]any{} },
		Reload: func() (bool, error) { return false, nil },
	})
	call := func(path, body string) (int, map[string]any) {
		method := "POST"
		if body == "" {
			method = "GET"
		}
		req := httptest.NewRequest(method, "/settings/api/"+path, strings.NewReader(body))
		req.Header.Set("X-Voice-Token", "tok")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	rev := func(st map[string]any) string { s, _ := st["characters_rev"].(string); return s }

	if code, _ := call("save", `{"characters":[{"name":"Anna","preset":"old"}]}`); code != 200 {
		t.Fatalf("first save: %d", code)
	}
	_, window := call("state", "")
	if rev(window) == "" {
		t.Fatal("state has no characters_rev")
	}

	// Someone else (an agent) edits the file directly.
	b, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(b), `"old"`, `"new"`, 1)), 0o644)

	// The window still shows the list as loaded; its save is refused.
	code, out := call("save", `{"characters":[{"name":"Anna","preset":"old"},{"name":"Luna"}],"characters_rev":"`+rev(window)+`"}`)
	if code != http.StatusConflict {
		t.Fatalf("stale save: %d, want 409", code)
	}
	b, _ = os.ReadFile(path)
	if !strings.Contains(string(b), `"new"`) || strings.Contains(string(b), "Luna") {
		t.Fatalf("stale save overwrote the file:\n%s", b)
	}
	// It gets the list as it is now, and a save from that goes through.
	fresh, _ := out["state"].(map[string]any)
	if chars, _ := fresh["characters"].([]any); len(chars) != 1 || chars[0].(map[string]any)["preset"] != "new" {
		t.Fatalf("conflict state characters = %v", fresh["characters"])
	}
	if code, _ := call("save", `{"characters":[{"name":"Anna","preset":"new"},{"name":"Luna"}],"characters_rev":"`+rev(fresh)+`"}`); code != 200 {
		t.Fatalf("fresh save: %d", code)
	}
	// A save without a revision (other windows, older pages) still works.
	if code, _ := call("save", `{"characters":[{"name":"Anna"}]}`); code != 200 {
		t.Fatalf("save without revision: %d", code)
	}
}
