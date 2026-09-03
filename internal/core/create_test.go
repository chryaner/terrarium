package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createFixture(t *testing.T) (*State, CreateOpts, map[string]bool) {
	t.Helper()
	iso := filepath.Join(t.TempDir(), "install.iso")
	if err := os.WriteFile(iso, []byte("not really an iso"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &State{
		Goldens: map[string]*Golden{},
		Envs:    map[string]*Env{"dev": {VMName: "trr-dev", Golden: "debian-12"}},
	}
	return st, CreateOpts{ISO: iso, OSType: "Linux_64", DiskGB: 32}, map[string]bool{"trr-taken": true}
}

func TestCreateChecks(t *testing.T) {
	st, o, vms := createFixture(t)
	vmName, err := createChecks(st, "suse11", o, vms)
	if err != nil {
		t.Fatal(err)
	}
	if vmName != "trr-suse11" {
		t.Errorf("vmName = %q", vmName)
	}
}

func TestCreateChecksRejects(t *testing.T) {
	st, good, vms := createFixture(t)
	missingISO := good
	missingISO.ISO = filepath.Join(t.TempDir(), "gone.iso")
	noType := good
	noType.OSType = ""
	noISO := good
	noISO.ISO = ""

	cases := []struct {
		name    string
		env     string
		opts    CreateOpts
		wantErr string
	}{
		{"bad name", "suse 11", good, "invalid env name"},
		{"existing env", "dev", good, `env "dev" already exists`},
		{"vm name taken", "taken", good, "already exists in VirtualBox"},
		{"no ostype", "suse11", noType, "--ostype is required"},
		{"no iso", "suse11", noISO, "--iso is required"},
		{"missing iso", "suse11", missingISO, "iso:"},
	}
	for _, c := range cases {
		_, err := createChecks(st, c.env, c.opts, vms)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.wantErr)
		}
	}
}

// An env with no golden is the whole point of `create`: it must not read as a
// broken fork, and the error it gives has to say how to get credentials.
func TestSSHTargetWithoutGolden(t *testing.T) {
	e := &Engine{St: &State{
		Goldens: map[string]*Golden{},
		Envs:    map[string]*Env{"suse11": {VMName: "trr-suse11", SSHPort: 42210}},
	}}
	_, _, _, _, err := e.SSHTarget("suse11")
	if err == nil {
		t.Fatal("an env with no golden has no credentials")
	}
	for _, want := range []string{"no credentials", "promote", "adopt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

// Promoting an ISO-installed env is how it gets a golden at all, so the
// missing source must not be an error.
func TestPromoteChecksWithoutGolden(t *testing.T) {
	st := &State{
		Goldens: map[string]*Golden{},
		Envs:    map[string]*Env{"suse11": {VMName: "trr-suse11"}},
	}
	env, g, err := promoteChecks(st, "suse11", "suse-11.4")
	if err != nil {
		t.Fatal(err)
	}
	if env.VMName != "trr-suse11" || g.VMName != "trr-golden-suse-11.4" || g.hasCreds() {
		t.Errorf("env %+v golden %+v", env, g)
	}
}
