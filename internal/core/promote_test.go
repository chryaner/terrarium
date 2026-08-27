package core

import (
	"strings"
	"testing"
)

func promoteState() *State {
	return &State{
		Goldens: map[string]*Golden{
			"debian-12": {
				VMName:  "trr-golden-debian-12",
				SSHUser: "terrarium",
				SSHKey:  `C:\keys\id_ed25519`,
			},
			"adopted-xp": {VMName: "xp-base"}, // credless
		},
		Envs: map[string]*Env{
			"dev": {VMName: "trr-dev", Golden: "debian-12", SSHPort: 42201},
			"old": {VMName: "trr-old", Golden: "adopted-xp", SSHPort: 42202},
		},
	}
}

func TestPromoteChecksInheritsCreds(t *testing.T) {
	env, g, err := promoteChecks(promoteState(), "dev", "team-dev")
	if err != nil {
		t.Fatal(err)
	}
	if env.VMName != "trr-dev" {
		t.Errorf("wrong env: %+v", env)
	}
	// The new golden's disk is a copy of the env's, so the env's credentials
	// are the ones that can log in to its forks.
	if g.VMName != "trr-golden-team-dev" || g.SSHUser != "terrarium" || g.SSHKey != `C:\keys\id_ed25519` {
		t.Errorf("golden should inherit the source golden's identity and creds: %+v", g)
	}
}

func TestPromoteChecksCredlessSource(t *testing.T) {
	_, g, err := promoteChecks(promoteState(), "old", "xp-tools")
	if err != nil {
		t.Fatal(err)
	}
	if g.hasCreds() {
		t.Errorf("a credless source must promote to a credless golden: %+v", g)
	}
}

func TestPromoteChecksRejects(t *testing.T) {
	cases := []struct {
		name     string
		env, img string
		wantErr  string
	}{
		{"missing env", "nope", "x", "no env"},
		{"existing golden", "dev", "debian-12", "already exists"},
		{"bad name", "dev", "team dev", "invalid golden name"},
		{"leading dash", "dev", "-x", "invalid golden name"},
	}
	for _, c := range cases {
		_, _, err := promoteChecks(promoteState(), c.env, c.img)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.wantErr)
		}
	}
}

// Golden names carry dots (ubuntu-24.04); env names never do, so the scratch
// env of a derived build has to translate.
func TestScratchName(t *testing.T) {
	if got := scratchName("team-1.2"); got != "team-1-2-build" {
		t.Errorf("scratchName = %q", got)
	}
	if !nameRe.MatchString(scratchName("ubuntu-24.04")) {
		t.Error("scratch name must be a valid env name")
	}
}

// Both checks fire before any VirtualBox call, so a nil driver proves they
// come first: removing a forked golden must never get as far as the VM.
func TestRemoveGoldenChecks(t *testing.T) {
	e := &Engine{St: promoteState()}
	if err := e.RemoveGolden("nope"); err == nil || !strings.Contains(err.Error(), "no golden") {
		t.Errorf("missing golden: got %v", err)
	}
	if err := e.RemoveGolden("debian-12"); err == nil || !strings.Contains(err.Error(), "forked by") {
		t.Errorf("golden with forks: got %v", err)
	}
}
