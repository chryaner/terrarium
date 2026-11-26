package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chryaner/terrarium/internal/paths"
)

// Doctor itself shells out to VBoxManage, so only the parts that do not are
// exercised here.

func TestDoctorOK(t *testing.T) {
	cases := []struct {
		name   string
		checks []Check
		want   bool
	}{
		{"all pass", []Check{{OK: true}, {OK: true}}, true},
		{"one fails", []Check{{OK: true}, {Name: "VBoxSVC responding"}}, false},
		{"only an optional one fails", []Check{{OK: true}, {Name: "ssh client", Optional: true}}, true},
		{"optional passes", []Check{{OK: true, Optional: true}}, true},
		{"nothing checked", nil, true},
	}
	for _, c := range cases {
		if got := DoctorOK(c.checks); got != c.want {
			t.Errorf("%s: DoctorOK = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStateDirCheck(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(paths.BaseEnv, dir)

	c := stateDirCheck()
	if !c.OK {
		t.Fatalf("a writable temp dir should pass: %+v", c)
	}
	if !strings.HasPrefix(c.Detail, dir) {
		t.Errorf("detail should name the directory, got %q", c.Detail)
	}
	// The probe must not survive the check.
	if entries, err := os.ReadDir(filepath.Join(dir, "terrarium")); err == nil {
		for _, e := range entries {
			if e.Name() == ".writable" {
				t.Error("the write probe was left behind")
			}
		}
	}
}

// Every failing check has to say what to do about it, or it is just bad news.
func TestFailingChecksCarryAFix(t *testing.T) {
	for _, c := range []Check{sshClientCheck(), stateDirCheck()} {
		if !c.OK && c.Fix == "" {
			t.Errorf("%s failed with no fix: %+v", c.Name, c)
		}
	}
}

func TestSSHClientCheckIsOptional(t *testing.T) {
	c := sshClientCheck()
	if c.Name != "ssh client" {
		t.Errorf("name: %q", c.Name)
	}
	// Missing ssh narrows terrarium to the built-in client rather than
	// stopping it, so it must never fail the overall verdict.
	if !c.OK && !c.Optional {
		t.Error("a missing ssh client must be optional, not fatal")
	}
	if !c.OK && !strings.Contains(c.Detail, "exec") {
		t.Errorf("detail should say what still works without it, got %q", c.Detail)
	}
}

func fakeModules(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.Mkdir(filepath.Join(dir, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLinuxKernelChecks(t *testing.T) {
	cases := []struct {
		name    string
		modules []string
		wantOK  bool // DoctorOK over the result
		wantKVM string
	}{
		{"driver loaded", []string{"vboxdrv"}, true, ""},
		{"driver missing", nil, false, ""},
		{"kvm intel only warns", []string{"vboxdrv", "kvm", "kvm_intel"}, true, "kvm_intel"},
		{"kvm amd only warns", []string{"vboxdrv", "kvm", "kvm_amd"}, true, "kvm_amd"},
	}
	for _, c := range cases {
		checks := linuxKernelChecks(fakeModules(t, c.modules...))
		if got := DoctorOK(checks); got != c.wantOK {
			t.Errorf("%s: DoctorOK = %v, want %v (%+v)", c.name, got, c.wantOK, checks)
		}
		var kvm *Check
		for i := range checks {
			if !checks[i].OK && checks[i].Fix == "" {
				t.Errorf("%s: %s failed with no fix", c.name, checks[i].Name)
			}
			if checks[i].Name == "KVM" {
				kvm = &checks[i]
			}
		}
		switch {
		case c.wantKVM == "" && kvm != nil:
			t.Errorf("%s: unexpected KVM warning %+v", c.name, *kvm)
		case c.wantKVM != "" && (kvm == nil || !kvm.Optional || !strings.Contains(kvm.Fix, c.wantKVM)):
			t.Errorf("%s: want an optional KVM warning naming %s, got %+v", c.name, c.wantKVM, kvm)
		}
	}
}

func TestFixTextsPerOS(t *testing.T) {
	if !strings.Contains(killVBoxSVC("windows"), "VBoxSVC.exe") || !strings.Contains(killVBoxSVC("linux"), "pkill") {
		t.Error("VBoxSVC fix should use each OS's own spelling")
	}
	if !strings.Contains(installSSH("windows"), "Optional features") || !strings.Contains(installSSH("linux"), "openssh-client") {
		t.Error("ssh install fix should name each OS's own route")
	}
}

func TestCPUVirtChecks(t *testing.T) {
	cases := []struct {
		name    string
		cpuinfo string
		want    string // "", "ok" or "fail"
	}{
		{"intel", "processor : 0\nflags\t\t: fpu vme vmx sse\n", "ok"},
		{"amd", "processor : 0\nflags : fpu svm sse\n", "ok"},
		{"second core has it", "flags : fpu\nflags : fpu vmx\n", "ok"},
		{"nested host without it", "processor : 0\nflags : fpu vme sse hypervisor\n", "fail"},
		{"a longer flag is not a match", "flags : fpu vmx_fake svmx\n", "fail"},
		{"no flags line, as on arm", "processor : 0\nFeatures : fp asimd\n", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		path := filepath.Join(t.TempDir(), "cpuinfo")
		if err := os.WriteFile(path, []byte(c.cpuinfo), 0o600); err != nil {
			t.Fatal(err)
		}
		checks := cpuVirtChecks(path)
		switch c.want {
		case "":
			if len(checks) != 0 {
				t.Errorf("%s: nothing to report, got %+v", c.name, checks)
			}
		case "ok":
			if len(checks) != 1 || !checks[0].OK {
				t.Errorf("%s: want one passing check, got %+v", c.name, checks)
			}
		case "fail":
			if len(checks) != 1 || checks[0].OK || checks[0].Optional || checks[0].Fix == "" {
				t.Errorf("%s: want one failing check with a fix, got %+v", c.name, checks)
			}
		}
	}
	if checks := cpuVirtChecks(filepath.Join(t.TempDir(), "absent")); len(checks) != 0 {
		t.Errorf("an unreadable cpuinfo proves nothing, got %+v", checks)
	}
}

func TestHypervisorChecksOnlyWarn(t *testing.T) {
	if checks := hypervisorChecks(false); len(checks) != 0 {
		t.Errorf("no hypervisor, nothing to say, got %+v", checks)
	}
	checks := hypervisorChecks(true)
	if len(checks) != 1 || checks[0].OK || !checks[0].Optional || checks[0].Fix == "" {
		t.Fatalf("want one optional warning with a fix, got %+v", checks)
	}
	if !DoctorOK(checks) {
		t.Error("an active Windows hypervisor must not fail doctor: everything still works")
	}
}
