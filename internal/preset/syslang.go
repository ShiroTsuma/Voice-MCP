package preset

import "strings"

// DefaultLanguages are the languages assumed before the user picks any: the
// system's (a code such as "pl-PL" or "pl_PL.UTF-8") and English.
func DefaultLanguages(system string) []string {
	code := strings.ToLower(strings.TrimSpace(system))
	if i := strings.IndexAny(code, "-_.@"); i >= 0 {
		code = code[:i]
	}
	if code == "" || code == "en" || code == "c" || code == "posix" {
		return []string{"en"}
	}
	return []string{code, "en"}
}
