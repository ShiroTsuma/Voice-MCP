package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"voice/internal/core"
)

const themeUsage = `voice theme install <folder> [-name n]   copy a reading window theme into voice
voice theme remove <name>                    delete an installed theme
voice theme list                             the installed themes
voice theme path                             the folder themes are installed in

A theme is a folder with a theme.css and what it uses (pictures, fonts).
Pick it in the settings under On screen, Reading window theme.
`

// themeName is what a theme folder may be called: it is part of a URL.
var themeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// runTheme manages the reading window's themes, so another package (a
// role-play bundle) can install its own without knowing voice's data folder.
func runTheme(args []string) error {
	if len(args) == 0 {
		fmt.Print(themeUsage)
		return nil
	}
	fs, cfgPath := flags("theme " + args[0])
	name := fs.String("name", "", "install: the theme's name (default: the folder's)")
	rest := args[1:]
	// Flags may come before or after the folder.
	var pos []string
	for len(rest) > 0 {
		fs.Parse(rest)
		rest = fs.Args()
		if len(rest) > 0 {
			pos, rest = append(pos, rest[0]), rest[1:]
		}
	}
	if _, err := loadConfig(*cfgPath, ""); err != nil {
		return err
	}
	dir := filepath.Join(core.DataDir(), "themes")
	switch args[0] {
	case "path":
		fmt.Println(dir)
		return nil
	case "list":
		for _, t := range installedThemes(dir) {
			fmt.Println(t)
		}
		return nil
	case "install":
		if len(pos) != 1 {
			return errors.New("theme install needs the theme's folder")
		}
		n, err := installTheme(dir, pos[0], *name)
		if err != nil {
			return err
		}
		fmt.Printf("installed theme %s in %s\n", n, filepath.Join(dir, n))
		return nil
	case "remove":
		if len(pos) != 1 || !themeName.MatchString(pos[0]) {
			return errors.New("theme remove needs an installed theme's name (voice theme list)")
		}
		p := filepath.Join(dir, pos[0])
		if _, err := os.Stat(filepath.Join(p, "theme.css")); err != nil {
			return fmt.Errorf("no theme %q in %s", pos[0], dir)
		}
		if err := os.RemoveAll(p); err != nil {
			return err
		}
		fmt.Printf("removed theme %s\n", pos[0])
		return nil
	}
	return fmt.Errorf("unknown theme command %q\n\n%s", args[0], themeUsage)
}

// installTheme copies the theme folder src into dir under name (src's own
// name when empty), replacing a theme of that name. It needs a theme.css and
// takes regular files only.
func installTheme(dir, src, name string) (string, error) {
	src, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(filepath.Join(src, "theme.css")); err != nil || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s has no theme.css", src)
	}
	if name == "" {
		name = filepath.Base(src)
	}
	if !themeName.MatchString(name) {
		return "", fmt.Errorf("a theme's name is letters, digits, dot, dash and underscore, not %q", name)
	}
	// Copy beside the old one first, so a failed copy leaves it as it was.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(dir, "."+name+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(tmp, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(to, 0o755)
		case !d.Type().IsRegular():
			return nil // links and devices stay behind
		}
		return copyFile(p, to)
	})
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, name)
	if err := os.RemoveAll(dst); err != nil {
		return "", err
	}
	return name, os.Rename(tmp, dst)
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// installedThemes are the folders in dir with a theme.css.
func installedThemes(dir string) []string {
	var out []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "theme.css")); e.IsDir() && err == nil && themeName.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}
