package preset

import "golang.org/x/sys/windows"

// systemLanguage is the Windows display language ("pl-PL"), or "".
func systemLanguage() string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(langs) == 0 {
		return ""
	}
	return langs[0]
}
