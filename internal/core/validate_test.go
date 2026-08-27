package core

import "testing"

// A newline here becomes an ssh_config directive of its own in the user's real
// config, and a ProxyCommand runs on the host.
func TestValidSSHUser(t *testing.T) {
	for _, ok := range []string{
		"terrarium", "root", "Administrator", "DOMAIN\\user",
		"ubuntu-user", "a.b_c", "",
	} {
		if !validSSHUser(ok) {
			t.Errorf("%q should be accepted", ok)
		}
	}

	for _, bad := range []string{
		"root\nProxyCommand calc.exe",
		"root\r\nProxyCommand calc.exe",
		"root\rProxyCommand calc.exe",
		"root\x00",
		"root\x1b[31m",
		"\n",
	} {
		if validSSHUser(bad) {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestSingleLineGuardsKeyPathsToo(t *testing.T) {
	if singleLine("C:\\keys\\id_ed25519\nProxyCommand calc.exe") {
		t.Error("a key path with a newline must be rejected as well")
	}
	if !singleLine(`C:\keys\id_ed25519`) {
		t.Error("an ordinary Windows path should be accepted")
	}
}
