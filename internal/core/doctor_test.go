package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	t.Setenv("LOCALAPPDATA", dir)

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
