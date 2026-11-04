package main

import "testing"

func TestOutcomeIsShort(t *testing.T) {
	for in, want := range map[string]string{
		"queued 70 characters for Clara (its Claude Code mod submits them)":                                                "Delivered to Clara",
		"queued 12 characters for Mira (it takes them through the session API)":                                            "Delivered to Mira",
		"queued 62 characters for Rebekka in Codex's queue":                                                                "Delivered to Rebekka",
		"queued 53 characters for Clara, busy (its next hook passes them on)":                                              "Queued for Clara, busy; it gets it with its next step",
		"queued 9 characters for Anna (idle in claude.exe with other sessions; it gets them with its next prompt or step)": "Queued for Anna; it gets it with its next prompt",
		"typed 12 characters into the console of Greta":                                                                    "Typed into Greta's console",
		"typed 12 characters into claude.exe for Diana":                                                                    "Typed into Diana's window",
		"typed 12 characters into codex.exe":                                                                               "Typed into codex.exe",
		"no agent took the text; it is on the clipboard":                                                                   "No session took it; it is on the clipboard",
	} {
		if got := outcome(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestSpokenControl(t *testing.T) {
	for in, want := range map[string]string{
		"Cancel.":                            "cancel",
		"Never mind!":                        "cancel",
		"Anuluj":                             "cancel",
		"Nieważne.":                          "cancel",
		"Cancel that order in the parser.":   "",
		"Nothing":                            "",
		"Add the export button. Start over.": "restart",
		"Od nowa":                            "restart",
		"Zrób deploy, zacznij od nowa":       "restart",
		"Rebekka, run the tests":             "",
		"Add the export button. Scrap it.":   "cancel",
		"Run the migration. Cancel it!":      "cancel",
		"Zrób deploy. Anuluj to.":            "cancel",
		"Restart the job and cancel it":      "",
		"The job hangs, cancel it.":          "",
	} {
		if got := spokenControl(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
