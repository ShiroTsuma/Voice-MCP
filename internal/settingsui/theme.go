package settingsui

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// themePath resolves "<theme>/<file>" inside dir, refusing anything that
// would leave the theme's folder.
func themePath(dir, rel string) (string, error) {
	if dir == "" || rel == "" || strings.Contains(rel, "\\") || strings.Contains(rel, ":") {
		return "", errors.New("no such theme file")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	parts := strings.SplitN(clean, string(filepath.Separator), 2)
	if len(parts) != 2 || parts[0] == ".." || parts[0] == "." || strings.HasPrefix(parts[1], "..") || filepath.IsAbs(clean) {
		return "", errors.New("no such theme file")
	}
	p := filepath.Join(dir, clean)
	root := filepath.Join(dir, parts[0]) + string(filepath.Separator)
	if !strings.HasPrefix(p, root) {
		return "", errors.New("no such theme file")
	}
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return "", errors.New("no such theme file")
	}
	return p, nil
}

// themes lists the folders in dir that have a theme.css.
func themes(dir string) []string {
	out := []string{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, e.Name(), "theme.css")); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	sort.Strings(out)
	return out
}
