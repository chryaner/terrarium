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

const testPubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJq4/0d0h7Zc0VLC9WcpqZ+Nn8Jf3Yy4L6xX9x0aBcDe terrarium"

// VirtualBox pastes the post-install into a .cmd, so a single metacharacter
// costs a forty-minute install to discover.
func TestWindowsPostInstallIsBatchSafe(t *testing.T) {
	cmd := windowsPostInstall(testPubKey, "terrarium", "pw")
	for _, c := range []string{"%", "&", "|", "<", ">", "^", "\r", "\n"} {
		if strings.Contains(cmd, c) {
			t.Errorf("windowsPostInstall contains cmd metacharacter %q", c)
		}
	}
	// One quoted -Command, so the inner quoting has to be single quotes.
	if strings.Count(cmd, `"`) != 2 {
		t.Errorf("expected exactly the one -Command quote pair, got %d quotes", strings.Count(cmd, `"`))
	}
	// VirtualBox truncates a long post-install command, and there is no
	// warning when it does.
	if len(cmd) > 1500 {
		t.Errorf("post-install is %d chars, too long to rely on VirtualBox passing it whole", len(cmd))
	}
}

// The two things that make a new golden usable the way the Linux ones are:
// key auth, and a shell that needs one layer of quoting instead of three.
func TestWindowsPostInstallInstallsKeyAndShell(t *testing.T) {
	cmd := windowsPostInstall(testPubKey, "terrarium", "pw")
	for _, want := range []string{
		testPubKey,
		// sshd reads this file, and only this file, for administrators.
		"administrators_authorized_keys",
		// and ignores it silently unless nobody else can write it
		"icacls",
		"/inheritance:r",
		`HKLM:\SOFTWARE\OpenSSH`,
		"DefaultShell",
		windowsDefaultShell,
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("post-install is missing %q:\n%s", want, cmd)
		}
	}
	// The key has to be written before its ACL is fixed, and both after the
	// capability that creates the directory.
	if strings.Index(cmd, "Add-WindowsCapability") > strings.Index(cmd, "Set-Content") ||
		strings.Index(cmd, "Set-Content") > strings.Index(cmd, "icacls") {
		t.Errorf("post-install steps are out of order:\n%s", cmd)
	}
}

// A fork nobody has logged into has no interactive session, so exec --desktop
// and screenshot have nothing to show. Winlogon logging the account in by
// itself is what gives every fork one.
func TestWindowsPostInstallSetsAutoLogon(t *testing.T) {
	cmd := windowsPostInstall(testPubKey, "terrarium", "pw")
	for _, want := range []string{
		winlogonKey,
		"AutoAdminLogon",
		"DefaultUserName -Value 'terrarium'",
		"DefaultPassword -Value 'pw'",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("post-install is missing %q: %s", want, cmd)
		}
	}
	// A quote in the password would end the PowerShell string it is pasted
	// into and leave the rest of the command as syntax.
	if got := windowsPostInstall(testPubKey, "u", "it's"); !strings.Contains(got, "DefaultPassword -Value 'it''s'") {
		t.Errorf("a quote in the password is not escaped: %s", got)
	}
}
