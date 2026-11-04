//go:build windows

package hotkey

import "testing"

func TestParse(t *testing.T) {
	cases := map[string][2]uint32{
		"Ctrl+Alt+Space":   {modControl | modAlt, 0x20},
		"ctrl + shift + m": {modControl | modShift, 'M'},
		"Win+F12":          {modWin, 0x7B},
		"Alt+`":            {modAlt, 0xC0},
	}
	for spec, want := range cases {
		mods, vk, err := Parse(spec)
		if err != nil || mods != want[0] || vk != want[1] {
			t.Errorf("Parse(%q) = %x %x %v, want %x %x", spec, mods, vk, err, want[0], want[1])
		}
	}
	for _, bad := range []string{"Space", "Ctrl+", "Ctrl+Foo", "M+Ctrl"} {
		if _, _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
}
