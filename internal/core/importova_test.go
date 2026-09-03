package core

import (
	"strings"
	"testing"
)

func TestImportChecks(t *testing.T) {
	st := &State{
		Goldens: map[string]*Golden{"win10": {VMName: "trr-golden-win10"}},
		Envs:    map[string]*Env{},
	}

	cases := []struct {
		name      string
		path      string
		image     string
		fileFound bool
		vmTaken   bool
		wantErr   string
	}{
		{name: "ova", path: `C:\d\noble.ova`, image: "noble", fileFound: true},
		{name: "ovf is the same appliance unpacked", path: `C:\d\noble.ovf`, image: "noble", fileFound: true},
		{name: "extension is case insensitive", path: `C:\d\NOBLE.OVA`, image: "noble", fileFound: true},
		{name: "dots allowed in a golden name", path: `C:\d\a.ova`, image: "centos-6.10", fileFound: true},

		{name: "no file", path: "", image: "noble", wantErr: "required"},
		{name: "not an appliance", path: `C:\d\noble.qcow2`, image: "noble", fileFound: true, wantErr: ".ova or .ovf"},
		{name: "missing file", path: `C:\d\gone.ova`, image: "noble", wantErr: "no such file"},
		{name: "bad golden name", path: `C:\d\a.ova`, image: "no spaces", fileFound: true, wantErr: "invalid golden name"},
		{name: "empty golden name", path: `C:\d\a.ova`, image: "", fileFound: true, wantErr: "invalid golden name"},
		{name: "golden taken", path: `C:\d\a.ova`, image: "win10", fileFound: true, wantErr: "already exists: pick another"},
		{name: "vm taken", path: `C:\d\a.ova`, image: "noble", fileFound: true, vmTaken: true, wantErr: "already exists in VirtualBox"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, err := importChecks(st, c.path, c.image, c.fileFound, c.vmTaken)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q", c.wantErr)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("error %q should contain %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if g.VMName != goldenPrefix+c.image {
				t.Errorf("vm name %q, want %q", g.VMName, goldenPrefix+c.image)
			}
		})
	}
}

// A rejected import must not have touched state: the checks run before
// anything is extracted, and they are the whole reason for that ordering.
func TestImportChecksWriteNothing(t *testing.T) {
	st := &State{Goldens: map[string]*Golden{}, Envs: map[string]*Env{}}
	if _, err := importChecks(st, `C:\d\a.ova`, "noble", true, false); err != nil {
		t.Fatal(err)
	}
	if len(st.Goldens) != 0 {
		t.Errorf("checks recorded a golden: %v", st.Goldens)
	}
}
