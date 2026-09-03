package core

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DefaultDiskGB is the disk a hand-installed machine gets. Dynamic, so an
// installer that uses a tenth of it costs a tenth of it.
const DefaultDiskGB = 32

// CreateOpts describes a machine to install by hand from an ISO.
type CreateOpts struct {
	ISO    string
	OSType string
	DiskGB int
	CPUs   int
	MemMB  int
}

// createChecks validates a create before anything is registered, so the rules
// are testable without VirtualBox. vmNames is what VirtualBox currently has,
// which is the one fact this cannot work out for itself.
func createChecks(st *State, name string, o CreateOpts, vmNames map[string]bool) (string, error) {
	if !nameRe.MatchString(name) {
		return "", fmt.Errorf("invalid env name %q (letters, digits, dashes)", name)
	}
	if st.Envs[name] != nil {
		return "", fmt.Errorf("env %q already exists", name)
	}
	if o.OSType == "" {
		return "", fmt.Errorf("an --ostype is required: `VBoxManage list ostypes` names them (Linux_64, Windows10_64, OpenSUSE_64)")
	}
	if o.ISO == "" {
		return "", fmt.Errorf("an --iso is required: this builds a blank machine to install one on")
	}
	fi, err := os.Stat(o.ISO)
	if err != nil {
		return "", fmt.Errorf("iso: %w", err)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("iso %s is a directory", o.ISO)
	}
	vmName := VMPrefix + name
	if vmNames[vmName] {
		return "", fmt.Errorf("%s already exists in VirtualBox", vmName)
	}
	return vmName, nil
}

// Create builds a blank VM with an ISO in its drive and boots it, for an OS
// with no cloud image and no unattended answer file - an old distribution, an
// installer that has to be answered by hand. The result is an env like any
// other except that it has no golden and therefore no credentials: it is
// driven through the console tools until the OS inside it is installed and
// can be promoted.
func (e *Engine) Create(name string, o CreateOpts, progress func(string)) (*Env, error) {
	vms, err := e.VB.ListVMs()
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(vms))
	for _, vm := range vms {
		existing[vm.Name] = true
	}
	vmName, err := createChecks(e.St, name, o, existing)
	if err != nil {
		return nil, err
	}
	port, err := e.freePort()
	if err != nil {
		return nil, err
	}

	progress(fmt.Sprintf("creating %s (%s, %d cpu, %d MB, %d GB disk)", vmName, o.OSType, o.CPUs, o.MemMB, o.DiskGB))
	// The one mutating step with nothing to roll back yet: no record exists,
	// and a failed createvm registers nothing.
	if err := e.VB.CreateVM(vmName, o.OSType); err != nil {
		return nil, err
	}

	// Recorded before the hardware is built, so the rollback below has
	// something to remove and `terrarium rm` can reach the VM if this process
	// dies first. An empty Golden is what marks an env with no credentials.
	env := &Env{VMName: vmName, SSHPort: port, Created: time.Now(), OSType: o.OSType}
	if vm, err := e.findVM(vmName); err == nil {
		env.UUID = vm.UUID
	}
	e.St.Envs[name] = env
	if err := e.St.Save(); err != nil {
		return nil, e.rollbackFork(name, vmName, err, progress)
	}
	if err := e.prepareCreate(env, o, progress); err != nil {
		rolled := e.rollbackFork(name, vmName, err, progress)
		e.forgetDisk(vmName)
		return nil, rolled
	}
	return env, nil
}

// prepareCreate gives a registered VM its hardware and boots it into the
// installer. Every error it returns is rolled back by Create.
func (e *Engine) prepareCreate(env *Env, o CreateOpts, progress func(string)) error {
	vmName := env.VMName
	if err := e.VB.ModifyCPUMem(vmName, o.CPUs, o.MemMB); err != nil {
		return err
	}
	folder, err := e.VB.MachineFolder()
	if err != nil {
		return err
	}
	disk := filepath.Join(folder, vmName, vmName+".vdi")
	if err := e.VB.CreateDynamicDisk(disk, o.DiskGB*1024); err != nil {
		return err
	}

	// Same reason installWindows splits here: XP-era Windows has no AHCI
	// driver and bluescreens 0x7B booting from a SATA disk.
	ctrl := diskController
	if isNT5(o.OSType) {
		ctrl = "IDE"
		err = e.VB.AddIDEController(vmName, ctrl)
	} else {
		err = e.VB.AddSATAController(vmName, ctrl, 2)
	}
	if err != nil {
		return err
	}
	if err := e.VB.AttachHDD(vmName, ctrl, 0, disk); err != nil {
		return err
	}
	if err := e.VB.AttachDVD(vmName, ctrl, 1, o.ISO); err != nil {
		return err
	}
	// Disk first so the finished install boots itself; the blank disk has no
	// boot sector, so the first boot falls through to the DVD. No eject step,
	// and revert lands back on the installer rather than on a dead machine.
	if err := e.VB.SetBootOrder(vmName, "disk", "dvd"); err != nil {
		return err
	}
	// Forwarded now rather than after the install: the port is already
	// reserved in state, and modifyvm needs the machine stopped.
	if err := e.VB.SetNATSSH(vmName, env.SSHPort); err != nil {
		return err
	}
	// An installer is driven by mouse as often as by keyboard, and the tablet
	// has to land before the clean snapshot so revert keeps it.
	if err := e.VB.EnableMouseTablet(vmName); err != nil {
		return err
	}

	progress("booting the installer")
	if err := e.VB.StartHeadless(vmName); err != nil {
		return err
	}
	// Nothing to wait for - there is no sshd in an installer - but
	// snapshotting the instant it starts would leave the revert target at the
	// BIOS.
	time.Sleep(settleTime)
	progress("snapshotting clean state (blank disk: revert restarts the install)")
	return e.VB.TakeSnapshot(vmName, cleanSnapshot)
}

// forgetDisk removes the blank disk a failed create may have left behind.
// unregistervm --delete-all only takes media that were attached to the VM; a
// disk created but not yet attached stays registered and on disk, and the
// next create of the same name then fails at createmedium because of it.
func (e *Engine) forgetDisk(vmName string) {
	folder, err := e.VB.MachineFolder()
	if err != nil {
		return
	}
	disk := filepath.Join(folder, vmName, vmName+".vdi")
	_ = e.VB.CloseMedium(disk)
	_ = os.Remove(disk)
	_ = os.Remove(filepath.Dir(disk))
}
