package core

import (
	"strings"
	"testing"
	"time"
)

func TestAuthOf(t *testing.T) {
	cases := []struct {
		name     string
		golden   *Golden
		wantMode string
		wantUser string
	}{
		{"key wins", &Golden{SSHUser: "terrarium", SSHKey: `C:\k\id_ed25519`}, AuthKey, "terrarium"},
		{"password", &Golden{SSHUser: "terrarium", SSHPassword: "1"}, AuthPassword, "terrarium"},
		{"no user", &Golden{}, AuthNone, ""},
		// A user with nothing to authenticate with cannot log in either.
		{"user only", &Golden{SSHUser: "root"}, AuthNone, "root"},
		{"no golden", nil, AuthNone, ""},
	}
	for _, c := range cases {
		mode, user := authOf(c.golden)
		if mode != c.wantMode || user != c.wantUser {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, mode, user, c.wantMode, c.wantUser)
		}
	}
}

func TestFormatInfoGolden(t *testing.T) {
	got := FormatInfo(Info{
		Name: "win10", Kind: "golden", VMName: "trr-golden-win10",
		UUID: "600b130f", State: "poweroff", OSType: "Windows10", Arch: "x86",
		CPUs: 4, MemoryMB: 4096, Snapshot: "terrarium-base",
		Auth: AuthPassword, SSHUser: "terrarium",
	})

	for _, want := range []string{
		"name:      win10",
		"ostype:    Windows10",
		"arch:      x86",
		"cpus:      4",
		"memory:    4096 MB",
		"snapshot:  terrarium-base",
		"auth:      password (user terrarium)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// Env-only fields have no place on a golden.
	for _, unwanted := range []string{"golden:", "ssh port:", "ttl:"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("golden report should not carry %q:\n%s", unwanted, got)
		}
	}
}

// adopt takes a VirtualBox VM name, so a hint that prints the golden's name
// where the VM name belongs is a command that fails when you paste it.
func TestAdoptHint(t *testing.T) {
	adopted := AdoptHint("centos7", "centos7")
	if !strings.HasPrefix(adopted, "terrarium adopt centos7 --user") {
		t.Errorf("an adopted VM needs no --name: %q", adopted)
	}
	built := AdoptHint("trr-golden-winxp", "winxp")
	if !strings.HasPrefix(built, "terrarium adopt trr-golden-winxp --name winxp --user") {
		t.Errorf("a built golden must name both the VM and the image: %q", built)
	}
}

// The credless case is the one a reader is most likely looking this up to fix,
// so it has to carry the command that fixes it.
func TestFormatInfoCredlessSaysHowToFixIt(t *testing.T) {
	hint := AdoptHint("trr-golden-winxp", "winxp")
	got := FormatInfo(Info{Name: "winxp", Kind: "golden", Auth: AuthNone, AuthHint: hint})
	if !strings.Contains(got, hint) {
		t.Errorf("credless golden should name the fix:\n%s", got)
	}

	// Nothing to record credentials on is a different answer from "add them".
	orphan := FormatInfo(Info{Name: "probe", Kind: "env", Auth: AuthNone})
	if strings.Contains(orphan, "terrarium adopt") {
		t.Errorf("an env with no golden has nothing to adopt:\n%s", orphan)
	}
}

func TestFormatInfoEnv(t *testing.T) {
	expires := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	got := FormatInfo(Info{
		Name: "t1", Kind: "env", VMName: "trr-t1", State: "running",
		OSType: "Debian_64", Arch: "x64", Snapshot: "clean",
		Auth: AuthKey, SSHUser: "terrarium",
		Golden: "debian-12", SSHPort: 42200, Expires: expires,
	})
	for _, want := range []string{"golden:", "debian-12", "ssh port:", "42200", "ttl:", "expires 2026-09-03T12:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	// No TTL is a normal state and says so, rather than showing a zero time.
	noTTL := FormatInfo(Info{Name: "t1", Kind: "env", Golden: "debian-12", Auth: AuthKey, SSHUser: "u"})
	if !strings.Contains(noTTL, "ttl:") || strings.Contains(noTTL, "0001-01-01") {
		t.Errorf("an env without a TTL should read as none:\n%s", noTTL)
	}
}

// An unknown guest type is reported as unknown rather than silently blank:
// a missing line reads as "there is nothing to know here".
func TestFormatInfoUnknownOSType(t *testing.T) {
	got := FormatInfo(Info{Name: "x", Kind: "golden", Auth: AuthNone})
	if !strings.Contains(got, "ostype:") || !strings.Contains(got, "unknown") {
		t.Errorf("an unprobed record should say unknown:\n%s", got)
	}
}
