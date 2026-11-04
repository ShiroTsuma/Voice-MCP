package inject

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A console program reads a line; ToConsole types it there from outside,
// with Polish letters and Enter. INJECT_PID=<pid> targets an existing process
// instead (a shell in Windows Terminal, for a manual check). Manual only
// (VOICE_INJECT_TEST=1): sandboxes and some CI sessions refuse AttachConsole.
func TestToConsoleTypesIntoAnotherConsole(t *testing.T) {
	if os.Getenv("VOICE_INJECT_TEST") == "" && os.Getenv("INJECT_PID") == "" {
		t.Skip("set VOICE_INJECT_TEST=1 to type into a new console")
	}
	text := "zażółć gęślą jaźń 42"
	if pid := os.Getenv("INJECT_PID"); pid != "" {
		n, _ := strconv.Atoi(pid)
		procFreeConsole.Call()
		if err := ToConsole(uint32(n), text, true); err != nil {
			t.Fatal(err)
		}
		return
	}
	out := filepath.Join(t.TempDir(), "line.txt")
	ready := out + ".ready"
	// Started like a shell in a terminal tab (start = ShellExecute), in its own
	// console window; it writes its PID when it is about to read a line.
	script := "[Console]::InputEncoding=[Text.Encoding]::UTF8; [IO.File]::WriteAllText($env:INJECT_READY, $PID); $l = [Console]::ReadLine(); [IO.File]::WriteAllText($env:INJECT_OUT, $l)"
	cmd := exec.Command("cmd", "/c", "start", "", "/min", "powershell", "-NoProfile", "-Command", script)
	cmd.Env = append(os.Environ(), "INJECT_OUT="+out, "INJECT_READY="+ready)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	var pid int
	for deadline := time.Now().Add(15 * time.Second); pid == 0; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the console program did not start")
		}
		b, _ := os.ReadFile(ready)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Kill()
	time.Sleep(300 * time.Millisecond) // from writing the file to waiting in ReadLine
	procFreeConsole.Call()             // the test's own console would block attaching
	if err := ToConsole(uint32(pid), text, true); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := p.Wait(); done <- err }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the console program never got Enter")
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != text {
		t.Fatalf("typed %q, got %q", text, got)
	}
}
