package core

import (
	"testing"
	"time"
)

// Whether a golden has usable SSH credentials decides whether fork/start wait
// for sshd or fall through to the console. An adopted XP VM has none.
func TestGoldenHasCreds(t *testing.T) {
	cases := []struct {
		name string
		g    *Golden
		want bool
	}{
		{"key", &Golden{SSHUser: "terrarium", SSHKey: `C:\keys\id_ed25519`}, true},
		{"password", &Golden{SSHUser: "Administrator", SSHPassword: "hunter2"}, true},
		{"user only", &Golden{SSHUser: "root"}, false},
		{"secret but no user", &Golden{SSHPassword: "hunter2"}, false},
		{"adopted bare VM", &Golden{VMName: "winxp", Snapshot: "terrarium-base"}, false},
		{"missing golden", nil, false},
	}
	for _, c := range cases {
		if got := c.g.hasCreds(); got != c.want {
			t.Errorf("%s: hasCreds() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStateRoundTrip(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())

	s, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Goldens) != 0 || len(s.Envs) != 0 {
		t.Fatal("fresh state must be empty")
	}

	s.Goldens["centos7"] = &Golden{VMName: "centos7", Snapshot: "terrarium-base", SSHUser: "root"}
	s.Envs["test"] = &Env{VMName: "trr-test", Golden: "centos7", SSHPort: 42200, Created: time.Now()}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	s2, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if s2.Goldens["centos7"] == nil || s2.Goldens["centos7"].SSHUser != "root" {
		t.Errorf("golden not restored: %+v", s2.Goldens)
	}
	if s2.Envs["test"] == nil || s2.Envs["test"].SSHPort != 42200 {
		t.Errorf("env not restored: %+v", s2.Envs)
	}
}

// The race a lone read-modify-write would lose: two processes load the same
// file, each adds a different env, and the second to save must not drop the
// first's addition.
func TestSaveKeepsConcurrentAddition(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	seed, _ := LoadState()
	seed.Goldens["g"] = &Golden{VMName: "g", Snapshot: "terrarium-base"}
	seed.Envs["a"] = &Env{VMName: "trr-a", Golden: "g", SSHPort: 42200}
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}

	first, _ := LoadState()
	stale, _ := LoadState() // both read {a} before either writes
	first.Envs["b"] = &Env{VMName: "trr-b", Golden: "g", SSHPort: 42201}
	stale.Envs["c"] = &Env{VMName: "trr-c", Golden: "g", SSHPort: 42202}

	if err := first.Save(); err != nil { // writes {a, b}
		t.Fatal(err)
	}
	if err := stale.Save(); err != nil { // must reach {a, b, c}, not {a, c}
		t.Fatal(err)
	}

	final, _ := LoadState()
	for _, name := range []string{"a", "b", "c"} {
		if final.Envs[name] == nil {
			t.Errorf("env %q was lost by a concurrent save", name)
		}
	}
}

// A stale writer's deletion must remove only what it deleted, leaving a
// concurrent addition in place.
func TestSaveMergesDeleteAgainstAddition(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	seed, _ := LoadState()
	seed.Goldens["g"] = &Golden{VMName: "g", Snapshot: "terrarium-base"}
	seed.Envs["a"] = &Env{VMName: "trr-a", Golden: "g", SSHPort: 42200}
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}

	adder, _ := LoadState()
	remover, _ := LoadState()
	adder.Envs["b"] = &Env{VMName: "trr-b", Golden: "g", SSHPort: 42201}
	delete(remover.Envs, "a")

	if err := adder.Save(); err != nil { // {a, b}
		t.Fatal(err)
	}
	if err := remover.Save(); err != nil { // deletes a, keeps b -> {b}
		t.Fatal(err)
	}

	final, _ := LoadState()
	if final.Envs["a"] != nil {
		t.Error("env a should have been deleted")
	}
	if final.Envs["b"] == nil {
		t.Error("env b (a concurrent addition) should have survived the delete")
	}
}

// An edit to a field must be written, while an env the process never touched
// keeps whatever the current file holds.
func TestSaveWritesOwnEditsOnly(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	seed, _ := LoadState()
	seed.Envs["a"] = &Env{VMName: "trr-a", SSHPort: 42200}
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}

	editor, _ := LoadState()
	editor.Envs["a"].RDPPort = 33890
	if err := editor.Save(); err != nil {
		t.Fatal(err)
	}

	final, _ := LoadState()
	if final.Envs["a"].RDPPort != 33890 {
		t.Errorf("edit not persisted: %+v", final.Envs["a"])
	}
}
