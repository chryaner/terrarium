package mcpserver

import (
	"testing"

	"github.com/chryaner/terrarium/internal/core"
)

// Running state comes from VBoxManage keyed by UUID, but records written
// before a UUID was captured only have a name to go on.
func TestDescribeEnv(t *testing.T) {
	env := &core.Env{
		VMName:  "trr-api",
		UUID:    "4402c3a8-7cae-48ec-aabf-0f6c03e76b98",
		Golden:  "ubuntu-24.04",
		SSHPort: 42201,
		Share:   `C:\work\api`,
	}

	got := describeEnv("api", env, map[string]bool{env.UUID: true})
	if !got.Running {
		t.Error("a UUID match means running")
	}
	if got.Name != "api" || got.VMName != "trr-api" || got.SSHPort != 42201 {
		t.Errorf("unexpected env: %+v", got)
	}
	if got.Share != `C:\work\api` {
		t.Errorf("the resolved host share should be reported, got %q", got.Share)
	}

	// Name fallback for a record with no UUID.
	noUUID := &core.Env{VMName: "trr-api", Golden: "g"}
	if !describeEnv("api", noUUID, map[string]bool{"trr-api": true}).Running {
		t.Error("a name match should still count as running")
	}

	if describeEnv("api", env, map[string]bool{}).Running {
		t.Error("nothing running means not running")
	}
	// A different VM being up must not mark this one running.
	if describeEnv("api", env, map[string]bool{"trr-other": true}).Running {
		t.Error("another VM's name should not match")
	}
}
