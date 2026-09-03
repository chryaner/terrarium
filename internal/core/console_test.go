package core

import "testing"

// Screenshot is read-only, so it takes anything running: the whole point is
// seeing a machine terrarium has not been told the credentials for yet.
func TestVMNameFor(t *testing.T) {
	st := &State{
		Goldens: map[string]*Golden{"win10": {VMName: "trr-golden-win10"}},
		Envs:    map[string]*Env{"probe": {VMName: "trr-probe"}},
	}

	cases := map[string]string{
		"probe": "trr-probe",
		"win10": "trr-golden-win10",
		// Unrecorded: taken as a raw VirtualBox VM name.
		"someone-elses-vm": "someone-elses-vm",
	}
	for name, want := range cases {
		if got := vmNameFor(st, name); got != want {
			t.Errorf("vmNameFor(%q) = %q, want %q", name, got, want)
		}
	}

	// An env and a golden sharing a name resolves to the env: it is the
	// disposable one.
	st.Goldens["probe"] = &Golden{VMName: "trr-golden-probe"}
	if got := vmNameFor(st, "probe"); got != "trr-probe" {
		t.Errorf("an env should win a name collision, got %q", got)
	}
}
