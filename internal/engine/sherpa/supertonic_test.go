package sherpa

import "testing"

// A Supertonic request is said in its own language, else the engine's, else
// English; regional and underscore forms count as the language, and one it
// does not speak is refused instead of passed on.
func TestSupertonicLang(t *testing.T) {
	for _, c := range []struct{ req, engine, want string }{
		{"pl", "", "pl"}, {"pl-PL", "", "pl"}, {"pl_PL", "", "pl"}, {"PL", "", "pl"},
		{"", "de", "de"}, {"auto", "de-DE", "de"}, {"", "", "en"}, {"auto", "auto", "en"},
	} {
		if got, err := supertonicLang(c.req, c.engine); err != nil || got != c.want {
			t.Errorf("supertonicLang(%q, %q) = %q, %v; want %q", c.req, c.engine, got, err, c.want)
		}
	}
	for _, bad := range []string{"zh", "he", `en"`} {
		if got, err := supertonicLang(bad, ""); err == nil {
			t.Errorf("supertonicLang(%q) = %q, want an error", bad, got)
		}
	}
}
