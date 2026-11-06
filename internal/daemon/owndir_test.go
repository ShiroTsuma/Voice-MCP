package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A background process does not keep its starter's folder in use.
func TestDetachStartsInTheExecutablesFolder(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	cmd := exec.Command(exe, "serve")
	detach(cmd)
	if want := filepath.Dir(exe); !samePath(cmd.Dir, want) {
		t.Errorf("Dir = %q, want %q", cmd.Dir, want)
	}

	chosen := t.TempDir()
	cmd = exec.Command(exe, "serve")
	cmd.Dir = chosen
	detach(cmd)
	if cmd.Dir != chosen {
		t.Errorf("a chosen folder was replaced: %q", cmd.Dir)
	}
}

func samePath(a, b string) bool {
	x, err1 := filepath.Abs(a)
	y, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && filepath.Clean(x) == filepath.Clean(y)
}
