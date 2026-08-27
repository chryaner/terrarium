package core

import (
	"strings"
	"testing"
)

func TestIsNT5(t *testing.T) {
	// The ostypes that need IDE, a key and no SSH.
	for _, ostype := range []string{"WindowsXP", "WindowsXP_64", "Windows2000"} {
		if !isNT5(ostype) {
			t.Errorf("%s should be treated as NT5", ostype)
		}
	}
	// Everything current takes the SATA + OpenSSH path.
	for _, ostype := range []string{"Windows10", "Windows10_64", "Windows11_64", "Windows7", "Linux_64", ""} {
		if isNT5(ostype) {
			t.Errorf("%s should not be treated as NT5", ostype)
		}
	}
}

// The post-install for a credless guest has to be batch-safe, since VirtualBox
// pastes it verbatim into a .cmd: no character cmd.exe would eat.
func TestNT5PostInstallIsBatchSafe(t *testing.T) {
	for _, c := range []string{"%", "&", "|", "<", ">", "^", "\r", "\n"} {
		if strings.Contains(nt5PostInstall, c) {
			t.Errorf("nt5PostInstall contains cmd metacharacter %q: %q", c, nt5PostInstall)
		}
	}
}
