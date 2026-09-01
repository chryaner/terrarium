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

// Which guests get a cmd.exe command line instead of POSIX-quoted argv. Both
// spellings have to match: recipes carry the createvm id, showvminfo answers
// with the description.
func TestIsWindowsGuest(t *testing.T) {
	for _, ostype := range []string{"Windows10_64", "Windows 10 (64-bit)", "WindowsXP", "Windows Server 2022 (64-bit)"} {
		if !isWindowsGuest(ostype) {
			t.Errorf("%s should be treated as a Windows guest", ostype)
		}
	}
	// An unknown guest type quotes for a POSIX shell: that is the reported bug.
	for _, ostype := range []string{"Ubuntu (64-bit)", "Linux_64", "RedHat_64", "Other", ""} {
		if isWindowsGuest(ostype) {
			t.Errorf("%s should not be treated as a Windows guest", ostype)
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
