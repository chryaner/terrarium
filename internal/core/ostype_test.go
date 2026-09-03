package core

import "testing"

// The suffix on the guest type id is the only place the architecture shows,
// and everything that reports or compares it reads it from here.
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
		if got := ArchOf(ostype); got != want {
			t.Errorf("ArchOf(%q) = %q, want %q", ostype, got, want)
		}
	}
}
