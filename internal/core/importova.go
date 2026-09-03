package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ovaExts are the appliance files VBoxManage import accepts. A .ovf is the
// same appliance unpacked, so both work.
var ovaExts = map[string]bool{".ova": true, ".ovf": true}

// importChecks validates an import before anything is extracted or
// registered, which for a multi-gigabyte appliance is minutes of work to
// throw away. Kept clear of VirtualBox and the filesystem - the caller passes
// what it found - so the rules are testable without either, like
// promoteChecks. The returned Golden is the record the import will fill in.
func importChecks(st *State, ovaPath, image string, fileFound, vmTaken bool) (*Golden, error) {
	switch {
	case ovaPath == "":
		return nil, fmt.Errorf("an appliance file is required")
	case !ovaExts[strings.ToLower(filepath.Ext(ovaPath))]:
		return nil, fmt.Errorf("%s is not an appliance: import takes a .ova or .ovf file", filepath.Base(ovaPath))
	case !fileFound:
		return nil, fmt.Errorf("no such file: %s", ovaPath)
	case !goldenNameRe.MatchString(image):
		return nil, fmt.Errorf("invalid golden name %q (letters, digits, dots, dashes)", image)
	case st.Goldens[image] != nil:
		return nil, fmt.Errorf("golden %q already exists: pick another name", image)
	case vmTaken:
		return nil, fmt.Errorf("%s already exists in VirtualBox", goldenPrefix+image)
	}
	return &Golden{VMName: goldenPrefix + image}, nil
}

// ImportOVA registers an appliance file as a golden. Unlike Get there is no
// recipe, no download and no cloud-init: a legacy export has no cloud-init to
// wait for, and waiting for one is exactly how importing a vendor appliance
// hangs. The VM is imported, snapshotted where it stands - powered off,
// untouched - and recorded as ours to delete, because terrarium created it.
//
// Credentials are optional. An appliance whose login nobody knows is imported
// without any and probed through the console; adopt records what worked.
func (e *Engine) ImportOVA(ovaPath, image, user, password, key string, cpus, memMB int, progress func(string)) (*Golden, error) {
	if !validSSHUser(user) {
		return nil, fmt.Errorf("ssh user contains an illegal character")
	}
	abs, err := filepath.Abs(ovaPath)
	if err != nil {
		return nil, err
	}
	_, statErr := os.Stat(abs)
	_, findErr := e.findVM(goldenPrefix + image)
	g, err := importChecks(e.St, abs, image, statErr == nil, findErr == nil)
	if err != nil {
		return nil, err
	}
	g.SSHUser, g.SSHPassword, g.SSHKey = user, password, key

	progress(fmt.Sprintf("importing %s as %s", filepath.Base(abs), g.VMName))
	if err := e.VB.ImportOVA(abs, g.VMName, cpus, memMB); err != nil {
		return nil, err
	}
	// Registered from here, so a failure is left for inspection rather than
	// deleted: the disk it extracted is the expensive part.
	out, err := e.recordGolden(image, g, progress)
	if err != nil {
		return nil, leftRegistered(g.VMName, err)
	}
	return out, nil
}
