package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/chryaner/terrarium/internal/vbox"
)

// Check is one thing that has to hold before terrarium can work.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	// Fix says what to do about a failure, and is empty when OK.
	Fix string `json:"fix,omitempty"`
	// Optional marks a check whose failure narrows what terrarium can do
	// rather than stopping it, so it does not count against DoctorOK.
	Optional bool `json:"optional,omitempty"`
}

// Doctor runs every readiness check. It never returns an error: a failed check
// is a result, not a failure of the checking.
func Doctor() []Check {
	checks := vboxChecks()
	return append(checks, stateDirCheck(), sshClientCheck())
}

// DoctorOK reports whether everything that matters passed.
func DoctorOK(checks []Check) bool {
	for _, c := range checks {
		if !c.OK && !c.Optional {
			return false
		}
	}
	return true
}

func vboxChecks() []Check {
	exe, err := vbox.Locate()
	if err != nil {
		// Everything below needs VBoxManage; running the rest would only
		// produce the same error three more times.
		return []Check{{
			Name:   "VBoxManage",
			Detail: err.Error(),
			Fix:    "install VirtualBox from https://www.virtualbox.org/, and put VBoxManage on PATH if it is installed somewhere unusual",
		}}
	}
	checks := []Check{{Name: "VBoxManage", OK: true, Detail: exe}}

	c := &vbox.Client{Exe: exe, Timeout: vbox.DefaultTimeout}
	if ver, err := c.Version(); err != nil {
		checks = append(checks, Check{
			Name:   "VirtualBox version",
			Detail: err.Error(),
			Fix:    "VBoxManage is present but will not run: check the VirtualBox installation is intact",
		})
	} else {
		checks = append(checks, Check{Name: "VirtualBox version", OK: true, Detail: ver})
	}

	if vms, err := c.ListVMs(); err != nil {
		checks = append(checks, Check{
			Name:   "VBoxSVC responding",
			Detail: err.Error(),
			Fix:    "try again in a moment; if it persists VirtualBox is wedged - close its GUI and kill VBoxSVC.exe",
		})
	} else {
		checks = append(checks, Check{
			Name:   "VBoxSVC responding",
			OK:     true,
			Detail: fmt.Sprintf("%d VM(s) registered", len(vms)),
		})
	}

	// Informational, and only when it is available: nothing depends on being
	// able to read it.
	if folder, err := c.MachineFolder(); err == nil {
		checks = append(checks, Check{
			Name: "default machine folder", OK: true, Detail: folder, Optional: true,
		})
	}
	return checks
}

// stateDirCheck is the one check that writes anything: it creates the data
// directory if missing and drops a probe file to prove it is writable. Both
// are things the next real command would do anyway.
func stateDirCheck() Check {
	dir, err := dataDir()
	if err != nil {
		return Check{
			Name:   "state directory",
			Detail: err.Error(),
			Fix:    "set LOCALAPPDATA to a writable directory",
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Check{
			Name:   "state directory",
			Detail: err.Error(),
			Fix:    "create " + dir + " by hand, or point LOCALAPPDATA somewhere writable",
		}
	}
	// The directory existing says nothing about whether we may write in it, so
	// probe with a real file.
	probe := filepath.Join(dir, ".writable")
	if err := os.WriteFile(probe, []byte("terrarium"), 0o600); err != nil {
		return Check{
			Name:   "state directory",
			Detail: err.Error(),
			Fix:    "grant your user write access to " + dir,
		}
	}
	os.Remove(probe)
	return Check{Name: "state directory", OK: true, Detail: dir}
}

func sshClientCheck() Check {
	path, err := exec.LookPath("ssh")
	if err != nil {
		return Check{
			Name:     "ssh client",
			Optional: true,
			Detail:   "not on PATH. Only `terrarium ssh` needs it; `terrarium exec` and the MCP tools use a built-in SSH client and work without it",
			Fix:      "install OpenSSH (Windows: Settings > Optional features > OpenSSH Client)",
		}
	}
	return Check{Name: "ssh client", OK: true, Detail: path}
}
