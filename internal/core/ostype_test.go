package core

import "testing"

// The bug this exists for: a recipe pinned to Windows10 installed a 32-bit
// guest from an x64 ISO, and nothing in the tool said so until setup failed.
func TestArchOf(t *testing.T) {
	cases := map[string]string{
		"Windows10":          "x86",
		"WindowsXP":          "x86",
		"Debian":             "x86",
		"Windows10_64":       "x64",
		"Debian_64":          "x64",
		"Ubuntu24_LTS_64":    "x64",
		"Debian_arm64":       "arm64",
		"Ubuntu24_LTS_arm64": "arm64",
		// Nothing recorded means nothing to say, not "assume 32-bit".
		"": "",
	}
	for ostype, want := range cases {
		if got := archOf(ostype); got != want {
			t.Errorf("archOf(%q) = %q, want %q", ostype, got, want)
		}
	}
}
