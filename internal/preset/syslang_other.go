//go:build !windows

package preset

import "os"

// systemLanguage is the locale's language ("pl_PL.UTF-8"), or "".
func systemLanguage() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}
