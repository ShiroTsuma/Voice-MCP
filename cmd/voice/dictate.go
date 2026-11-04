package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"voice/internal/core"
	"voice/internal/hotkey"
	"voice/internal/inject"
	"voice/internal/listen"
	"voice/internal/overlay"
	"voice/internal/service"
)

// Desktop apps that host an agent (lower-case executable names): an agent
// running inside one gets dictation typed into the app's window.
var agentApps = map[string]bool{"codex.exe": true, "claude.exe": true, "code.exe": true, "cursor.exe": true, "windsurf.exe": true}

// chatApps are the agent apps whose window in front takes dictation when no
// known agent did: in an editor the focus may be a file, not a chat box.
var chatApps = map[string]bool{"codex.exe": true, "claude.exe": true}

// dictate starts recording on the first press and ends it on the second; the
// recognized text goes to an agent (deliver). Esc while it records or writes
// down cancels it; so does a message that is only "cancel", and one that
// ends in "start over" listens again (spokenControl).
func (c *controller) dictate() {
	c.mu.Lock()
	if c.dictating != nil {
		close(c.dictating)
		c.dictating = nil
		c.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	c.dictating = stop
	c.mu.Unlock()
	// Where the user was when they pressed it, before chimes or focus changes.
	hwnd, fgPID := inject.Foreground()
	cancelled := new(atomic.Bool)
	go func() {
		restart := false
		defer func() {
			if restart {
				c.dictate()
			}
		}()
		defer func() {
			c.mu.Lock()
			if c.dictating == stop {
				c.dictating = nil
			}
			if c.dictCancel == cancelled {
				c.dictCancel = nil
			}
			c.mu.Unlock()
			c.keys.Register(hotkeyCancel, "", nil)
		}()
		c.mu.Lock()
		c.dictCancel = cancelled
		c.mu.Unlock()
		if err := c.keys.RegisterKey(hotkeyCancel, hotkey.Escape, c.cancelDictation); err != nil {
			logf("dictation: Esc does not cancel: %v", err)
		}
		if !c.l.Available() {
			c.problem("no speech recognition model; run `voice models pull vad asr-parakeet`")
			return
		}
		c.sp.Stop() // do not record the agent talking
		screen, _ := c.feedback()
		logf("dictation: recording (overlay %v, on screen %v)", c.ov != nil, screen)
		c.show(func(s *overlay.State) { s.Recording, s.Level = true, 0 })
		defer c.show(func(s *overlay.State) { s.Recording, s.Transcribing = false, false })
		c.setDictation("listening", 0)
		defer c.setDictation("", 0)
		// A thinking pause in a dictated message is longer than in an answer.
		res, err := c.l.Listen(context.Background(), listen.Options{
			Wait: 10 * time.Second, MaxSpeech: 2 * time.Minute, EndSilence: 2, Stop: stop,
			Level: func(v float64) {
				c.show(func(s *overlay.State) { s.Level = v })
				c.setDictation("listening", v)
			},
			Recorded: func() {
				c.show(func(s *overlay.State) { s.Recording, s.Transcribing = false, true })
				c.setDictation("transcribing", 0)
			},
			Partial: c.setDictationText,
		})
		if cancelled.Load() {
			c.cancelled("Esc")
			return
		}
		if err != nil {
			c.problem("%v", err)
			return
		}
		text := strings.Join(strings.Fields(res.Text), " ") // a line break would send it early
		if text == "" {
			c.problem("%s", res.Reason)
			return
		}
		switch spokenControl(text) {
		case "cancel":
			c.cancelled("said " + strconv.Quote(text))
			return
		case "restart":
			logf("dictation: starting over (said %q)", text)
			restart = true
			return
		}
		c.deliver(text, hwnd, fgPID)
	}()
}

// feedback is how dictation reports what happened: "screen" (the overlay,
// the default), "voice" (short spoken lines) or "both".
func (c *controller) feedback() (screen, voice bool) {
	switch c.cfg.Serve.DictateFeedback {
	case "voice":
		return false, true
	case "both":
		return true, true
	}
	return true, false
}

// show changes the overlay, when dictation reports on screen.
func (c *controller) show(change func(*overlay.State)) {
	if screen, _ := c.feedback(); screen {
		c.ov.Set(change)
	}
}

// say speaks a short report, when dictation reports by voice.
func (c *controller) say(text string) {
	if _, voice := c.feedback(); voice {
		c.sp.Speak(core.Request{Text: text, Intent: core.IntentInfo, Lang: "en"})
	}
}

// flash shows an outcome on the overlay for a moment.
func (c *controller) flash(kind overlay.Kind) {
	c.show(func(s *overlay.State) { s.Flash, s.FlashUntil = kind, time.Now().Add(1600*time.Millisecond) })
}

// problem logs why dictation stopped and shows it.
func (c *controller) problem(format string, args ...any) {
	logf("dictation: "+format, args...)
	c.flash(overlay.Problem)
}

// queueChanged shows how many dictated messages wait for busy agents, and the
// moment a hook took some.
func (c *controller) queueChanged(waiting int, delivered bool) {
	c.show(func(s *overlay.State) { s.Queued = waiting })
	if delivered {
		c.flash(overlay.Done)
		c.say("Delivered.")
	}
}

// Shells and terminals: an agent below one of them runs in a terminal, even
// inside an editor (VS Code's integrated terminal), and reads its console.
var terminals = map[string]bool{"pwsh.exe": true, "powershell.exe": true, "cmd.exe": true, "bash.exe": true, "wsl.exe": true,
	"windowsterminal.exe": true, "openconsole.exe": true, "conhost.exe": true, "wezterm-gui.exe": true, "alacritty.exe": true}

// hostedBy is the desktop app an agent process runs inside, with no shell or
// terminal in between (Claude Code in the Claude app, Codex in the Codex
// app): it reads its input from the app, not from its console.
func hostedBy(pid uint32) uint32 {
	for _, p := range inject.Ancestors(pid) {
		name := strings.ToLower(inject.ProcessName(p))
		if terminals[name] {
			return 0
		}
		if agentApps[name] {
			return p
		}
	}
	return 0
}

// deliver hands text to the agent the user works with, most recently active
// first, or, with routing on, to the agent the message names ("Annie, ...",
// "Codex, ...", "mcp tools: ...") or to all of them ("wszyscy: ..."). A busy
// agent whose hooks pass dictation on gets it queued for its next step; an
// idle agent gets it typed: into its desktop app's window, brought to the
// front, or into its terminal console. The clipboard is the last resort.
// The transcript records what was said, to whom, and what became of it.
func (c *controller) deliver(text string, hwnd uintptr, fgPID uint32) string {
	sent, to, out := c.deliverText(text, hwnd, fgPID)
	c.agents.Record(service.Line{Kind: "dictated", PID: to, Text: sent, Outcome: outcome(out)})
	return out
}

// deliverText is deliver's work: it returns the text as sent (without the
// spoken address), the session it reached (0 for none or for everyone) and
// the report.
func (c *controller) deliverText(text string, hwnd uintptr, fgPID uint32) (string, uint32, string) {
	sv := c.cfg.Serve
	// Sessions the user excluded from dictation never get it.
	var open []service.AgentInfo
	var candidates []uint32
	for _, a := range c.agents.Info() {
		if !a.NoInput {
			open = append(open, a)
			candidates = append(candidates, a.PID)
		}
	}
	routed, all := false, false
	if sv.DictateRouting != nil && *sv.DictateRouting {
		if to, rest, everyone := service.Route(text, open); len(to) > 0 {
			candidates, text, routed, all = to, rest, true, everyone
		}
	}
	if all {
		var reports []string
		for _, pid := range candidates {
			if r, ok := c.deliverTo(pid, text, hwnd, fgPID, true); ok {
				reports = append(reports, r)
			}
		}
		if len(reports) > 0 {
			c.flash(overlay.Done)
			return text, 0, report("to everyone: %s", strings.Join(reports, "; "))
		}
	} else {
		for _, pid := range candidates {
			if r, ok := c.deliverTo(pid, text, hwnd, fgPID, false); ok {
				if !c.agents.Busy(pid) {
					c.flash(overlay.Done)
				}
				return text, pid, report("%s", r)
			}
		}
	}
	target := sv.DictateTarget
	app := strings.ToLower(inject.ProcessName(fgPID))
	// No known agent took it: the window in front, when it is an agent's chat
	// app or the target says so (not for a message addressed to someone else).
	if !routed && hwnd != 0 && (target == "window" || chatApps[app]) {
		if err := inject.ToWindow(hwnd, text, c.submit()); err == nil {
			c.flash(overlay.Done)
			return text, 0, report("typed %d characters into %s", len([]rune(text)), dirOr(app, "the window in front"))
		} else {
			logf("dictation: %s: %v", dirOr(app, "window"), err)
		}
	}
	c.flash(overlay.Problem)
	if err := inject.ToClipboard(text); err != nil {
		return text, 0, report("%v; the text was: %s", err, text)
	}
	c.say("Your message is on the clipboard.")
	return text, 0, report("no agent took the text; it is on the clipboard")
}

func report(format string, args ...any) string {
	msg := fmt.Sprintf(format, args...)
	logf("dictation: %s", msg)
	return msg
}

func (c *controller) submit() bool {
	return c.cfg.Serve.DictateSubmit != nil && *c.cfg.Serve.DictateSubmit
}

// deliverTo gives text to one agent and says how; ok is false when it could
// not (gone, no window, the target setting excludes it). broadcast types only
// into consoles: windows would steal the focus from each other.
func (c *controller) deliverTo(pid uint32, text string, hwnd uintptr, fgPID uint32, broadcast bool) (string, bool) {
	if !c.agents.Matches(pid) { // ended, or its PID now belongs to another program
		c.agents.Forget(pid)
		return "", false
	}
	target := c.cfg.Serve.DictateTarget
	host := hostedBy(pid)
	who := dirOr(c.agents.Name(pid), fmt.Sprintf("agent %d", pid))
	n := len([]rune(text))
	if target == "window" && host != fgPID {
		return "", false // only the window in front
	}
	if c.agents.Busy(pid) {
		c.agents.Enqueue(pid, text)
		c.say("Passing it on.")
		return fmt.Sprintf("queued %d characters for %s, busy (its next hook passes them on)", n, who), true
	}
	if c.agents.HasMod(pid) {
		// voice's Claude Code mod submits it as a prompt, on screen or not; an
		// agent on the session API takes it with its next poll.
		c.agents.Enqueue(pid, text)
		if c.agents.Client(pid) != "claude" {
			return fmt.Sprintf("queued %d characters for %s (it takes them through the session API)", n, who), true
		}
		return fmt.Sprintf("queued %d characters for %s (its Claude Code mod submits them)", n, who), true
	}
	if thread := c.agents.CodexThread(pid); thread != "" {
		// Codex's own queue: the app (or the CLI) holding the thread starts a
		// turn with it, on screen or not. Messages that waited go along.
		taken := c.agents.Take(pid)
		message := strings.Join(append(taken, text), " ")
		if err := codexQueue(pid, thread, message); err == nil {
			c.say("Queued for " + who + ".")
			return fmt.Sprintf("queued %d characters for %s in Codex's queue", n, who), true
		} else {
			logf("dictation: %s: codex queue: %v", who, err)
			for _, m := range taken {
				c.agents.Enqueue(pid, m)
			}
		}
	}
	if host != 0 {
		// A desktop app shows one conversation at a time: typing would land in
		// whichever is open, not necessarily this one. When the app holds other
		// sessions too (or for everyone), the message waits in the queue and
		// reaches this session with its next prompt or step.
		if broadcast || c.sharesApp(pid, host) {
			c.agents.Enqueue(pid, text)
			c.say("Queued for " + who + ".")
			return fmt.Sprintf("queued %d characters for %s (idle in %s with other sessions; it gets them with its next prompt or step)", n, who, inject.ProcessName(host)), true
		}
		if target == "console" {
			return "", false
		}
	}
	if host == 0 && target == "window" {
		return "", false
	}
	// Messages queued for a turn that ended without its hook go first; they go
	// back to the queue when typing fails, so nothing is lost.
	taken := c.agents.Take(pid)
	message := strings.Join(append(taken, text), " ")
	failed := func(format string, args ...any) (string, bool) {
		logf("dictation: "+format, args...)
		for _, m := range taken {
			c.agents.Enqueue(pid, m)
		}
		return "", false
	}
	if host != 0 {
		win := inject.MainWindow(host)
		if win == 0 {
			return failed("%s: its app (%s) has no window open", who, inject.ProcessName(host))
		}
		if err := inject.Focus(win); err != nil {
			return failed("%s: %v", who, err)
		}
		time.Sleep(150 * time.Millisecond) // the app moves the keyboard focus back to its input
		if err := inject.ToWindow(win, message, c.submit()); err != nil {
			return failed("%s (%s): %v", who, inject.ProcessName(host), err)
		}
		return fmt.Sprintf("typed %d characters into %s for %s", n, inject.ProcessName(host), who), true
	}
	if err := inject.ToConsole(pid, message, c.submit()); err != nil {
		return failed("%s: %v", who, err)
	}
	return fmt.Sprintf("typed %d characters into the console of %s", n, who), true
}

// liveAgents lists the sessions dictation can reach, for the settings, and
// forgets the ones whose process ended.
func (c *controller) liveAgents() []service.AgentInfo {
	out := []service.AgentInfo{}
	for _, a := range c.agents.Info() {
		if !c.agents.Matches(a.PID) {
			c.agents.Forget(a.PID)
			continue
		}
		out = append(out, a)
	}
	return out
}

// sessionLines describes the sessions for the tray: "Annie · Claude · MCP-Tools · working".
func sessionLines(agents []service.AgentInfo) []string {
	var out []string
	for _, a := range agents {
		parts := []string{dirOr(a.Name, "Agent")}
		switch c := strings.ToLower(a.Client); {
		case strings.Contains(c, "claude"):
			parts = append(parts, "Claude")
		case strings.Contains(c, "codex"):
			parts = append(parts, "Codex")
		case c != "":
			parts = append(parts, a.Client)
		}
		if a.Project != "" {
			parts = append(parts, a.Project)
		}
		state := "waiting for you"
		if a.Busy {
			state = "working"
		}
		if a.Waiting > 0 {
			state += fmt.Sprintf(", %d queued", a.Waiting)
		}
		out = append(out, strings.Join(append(parts, state), " · "))
	}
	return out
}

// overlayThemes lists the overlay's looks for the settings.
func overlayThemes() []map[string]string {
	var out []map[string]string
	for _, t := range overlay.Themes {
		out = append(out, map[string]string{"name": t.Name, "label": t.Label})
	}
	return out
}

// applyTheme switches the overlay's look; after a change it plays a short
// demo (two waiting messages, then their delivery) so the user sees it.
func (c *controller) applyTheme() {
	name := c.cfg.Serve.DictateTheme
	c.mu.Lock()
	changed := c.theme != "" && c.theme != name
	c.theme = name
	c.mu.Unlock()
	c.ov.SetTheme(name)
	c.ov.SetScale(floatOr(c.cfg.Serve.DictateOverlayScale, 1.5))
	if !changed {
		return
	}
	c.ov.Set(func(s *overlay.State) { s.Queued = 2 })
	time.AfterFunc(1800*time.Millisecond, func() {
		waiting := c.waitingMessages() // back to the real queue
		c.ov.Set(func(s *overlay.State) {
			s.Queued = waiting
			s.Flash, s.FlashUntil = overlay.Done, time.Now().Add(1600*time.Millisecond)
		})
	})
}

// waitingMessages counts the dictated messages that really wait.
func (c *controller) waitingMessages() int {
	n := 0
	for _, a := range c.agents.Info() {
		n += a.Waiting
	}
	return n
}

// sharesApp reports whether another live session runs in the same desktop app
// as pid, so typing into the app's window could reach the wrong one.
func (c *controller) sharesApp(pid, host uint32) bool {
	for _, a := range c.agents.Info() {
		if a.PID != pid && c.agents.Matches(a.PID) && hostedBy(a.PID) == host {
			return true
		}
	}
	return false
}

// codexQueue hands message to Codex's queue for thread through the Codex
// that runs it (the app's own codex.exe, or the CLI's), so the command
// matches the version that reads the queue.
func codexQueue(pid uint32, thread, message string) error {
	exe := inject.ProcessPath(pid)
	if !strings.EqualFold(filepath.Base(exe), "codex.exe") {
		var err error
		if exe, err = exec.LookPath("codex"); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "queue", "--thread", thread, "--message", message)
	hidden(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// dictation is what the settings window shows while the user dictates.
type dictation struct {
	State string    `json:"state"` // listening | transcribing
	Since time.Time `json:"since"`
	Level float64   `json:"level"` // the microphone, 0..1, while listening
	// Text is what was heard so far, phrase by phrase, before the final
	// transcription.
	Text string `json:"text,omitempty"`
}

// setDictation records dictation's state for the settings window; "" ends it.
func (c *controller) setDictation(state string, level float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if state != c.dict.State {
		c.dict.Since = time.Now()
		if fn := c.onDictating; fn != nil && (state == "") != (c.dict.State == "") {
			go fn(state != "")
		}
	}
	c.dict.State, c.dict.Level = state, level
	if state == "" {
		c.dict.Text = ""
	}
}

// setDictationText shows the words heard so far.
func (c *controller) setDictationText(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dict.State != "" {
		c.dict.Text = text
	}
}

// Dictation is the dictation in progress, nil when there is none.
func (c *controller) Dictation() *dictation {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dict.State == "" {
		return nil
	}
	d := c.dict
	return &d
}

// outcomes turn a dictation report (written for the log) into the short
// sentence the settings window shows: who got it and how.
var outcomes = []struct {
	re  *regexp.Regexp
	out string
}{
	{regexp.MustCompile(`^queued \d+ characters for (.+), busy`), "Queued for $1, busy; it gets it with its next step"},
	{regexp.MustCompile(`^queued \d+ characters for (.+) \(its Claude Code mod`), "Delivered to $1"},
	{regexp.MustCompile(`^queued \d+ characters for (.+) \(it takes them through the session API`), "Delivered to $1"},
	{regexp.MustCompile(`^queued \d+ characters for (.+) in Codex's queue`), "Delivered to $1"},
	{regexp.MustCompile(`^queued \d+ characters for (.+) \(idle in`), "Queued for $1; it gets it with its next prompt"},
	{regexp.MustCompile(`^typed \d+ characters into the console of (.+)`), "Typed into $1's console"},
	{regexp.MustCompile(`^typed \d+ characters into \S+ for (.+)`), "Typed into $1's window"},
	{regexp.MustCompile(`^typed \d+ characters into (.+)`), "Typed into $1"},
	{regexp.MustCompile(`^to everyone: `), "Sent to everyone"},
	{regexp.MustCompile(`on the clipboard`), "No session took it; it is on the clipboard"},
}

func outcome(report string) string {
	for _, o := range outcomes {
		if m := o.re.FindStringSubmatchIndex(report); m != nil {
			return string(o.re.ExpandString(nil, o.out, report, m))
		}
	}
	return report
}

// cancelDictation is Esc while dictating: the recording stops and what was
// said goes nowhere.
func (c *controller) cancelDictation() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dictCancel != nil {
		c.dictCancel.Store(true)
	}
	if c.dictating != nil {
		close(c.dictating)
		c.dictating = nil
	}
}

// cancelled reports a dictation the user called off.
func (c *controller) cancelled(how string) {
	logf("dictation: cancelled (%s)", how)
	c.flash(overlay.Problem)
	c.say("Cancelled.")
}

// Messages that steer dictation instead of going to an agent: the whole
// message "cancel" (in English or Polish) drops it; one that ends in "start
// over" drops it and listens again.
var (
	cancelPhrases = map[string]bool{"cancel": true, "never mind": true, "nevermind": true, "scratch that": true,
		"forget it": true, "cancel that": true, "cancel it": true, "scrap it": true, "scrap that": true,
		"discard it": true, "discard that": true, "delete that": true,
		"anuluj": true, "anuluj to": true, "niewazne": true, "zapomnij": true, "zapomnij o tym": true, "skasuj to": true}
	restartPhrases = []string{"start over", "start again", "zacznij od nowa", "od nowa"}
)

// spokenControl finds a spoken command in a dictation: "cancel" when the
// whole of it, or its last sentence, is a cancel phrase ("Add a button. Scrap
// it."), so a sentence that only ends with the words ("restart the job and
// cancel it") is still delivered; "restart" when it ends with a restart phrase.
func spokenControl(text string) string {
	plain := plainWords(text)
	if cancelPhrases[plain] {
		return "cancel"
	}
	sentences := strings.FieldsFunc(text, func(r rune) bool { return r == '.' || r == '!' || r == '?' })
	for i := len(sentences) - 1; i >= 0; i-- {
		if last := plainWords(sentences[i]); last != "" {
			if len(sentences) > 1 && cancelPhrases[last] {
				return "cancel"
			}
			break
		}
	}
	for _, p := range restartPhrases {
		if plain == p || strings.HasSuffix(plain, " "+p) {
			return "restart"
		}
	}
	return ""
}

// plainWords lowers the text to its words, without punctuation or Polish
// diacritics.
func plainWords(text string) string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && r != '\'' })
	for i, w := range words {
		words[i] = strings.NewReplacer("ą", "a", "ć", "c", "ę", "e", "ł", "l", "ń", "n", "ó", "o", "ś", "s", "ź", "z", "ż", "z").Replace(w)
	}
	return strings.Join(words, " ")
}
