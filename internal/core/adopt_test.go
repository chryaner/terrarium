package core

import (
	"strings"
	"testing"
)

func adoptState() *State {
	return &State{
		Goldens: map[string]*Golden{
			"winxp": {VMName: "xp-base"},
		},
		Envs: map[string]*Env{},
	}
}

// Which golden name an adopt records under. Getting this wrong gives one VM
// two records, and `rm --golden` on either would take the other's forks.
func TestAdoptChecksResolvesTheGoldenName(t *testing.T) {
	cases := []struct {
		name string
		vm   string
		opts AdoptOpts
		want string
	}{
		{name: "a new VM is recorded under its own name", vm: "centos7", want: "centos7"},
		{
			name: "an already recorded VM keeps the name it has",
			vm:   "xp-base", want: "winxp",
		},
		{
			name: "--name wins",
			vm:   "xp-base", opts: AdoptOpts{Image: "winxp-sp3"}, want: "winxp-sp3",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := adoptChecks(adoptState(), c.vm, c.opts)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestAdoptChecksRejects(t *testing.T) {
	cases := []struct {
		name    string
		opts    AdoptOpts
		wantErr string
	}{
		// A newline would break the ssh-config block the user's name goes in.
		{"an ssh user with a line break in it", AdoptOpts{User: "root\nHost *"}, "illegal character"},
		{"a shell nothing can quote for", AdoptOpts{Shell: "bash"}, "unknown shell"},
		{"a transport that does not exist", AdoptOpts{Transport: "winrm"}, "unknown transport"},
		{"a golden name with a space", AdoptOpts{Image: "win xp"}, "invalid golden name"},
		// Guest Additions authenticate with a password and nothing else, so a
		// record with only a key could never log in.
		{
			"guestcontrol with a user and no password",
			AdoptOpts{Transport: TransportGuestControl, User: "admin", Key: `C:\k\id`},
			"pass --password",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := adoptChecks(adoptState(), "xp-base", c.opts)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("got %v, want it to mention %q", err, c.wantErr)
			}
		})
	}
}

// Adopting with nothing recorded is the first half of working out an unknown
// login, not a mistake.
func TestAdoptChecksAllowsNoCredentials(t *testing.T) {
	if _, err := adoptChecks(adoptState(), "some-vm", AdoptOpts{}); err != nil {
		t.Errorf("a credentialless adopt should be allowed: %v", err)
	}
}
