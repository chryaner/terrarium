package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chryaner/terrarium/internal/recipe"
	"github.com/chryaner/terrarium/internal/seed"
	"github.com/chryaner/terrarium/internal/sshx"
	"github.com/chryaner/terrarium/internal/vbox"
)

// windowsPostInstall makes a fresh Windows reachable the only way terrarium
// talks to guests. VirtualBox pastes this verbatim into a batch file it runs
// at first logon, so it must contain nothing cmd.exe would eat: no %, &, |,
// <, > or ^. A single quoted PowerShell -Command clears that bar.
//
// The account VirtualBox creates is a local administrator and the first-logon
// commands run with its elevated token, which Add-WindowsCapability needs.
// The capability installer usually adds the firewall rule itself; usually is
// not good enough for something that decides whether the golden is reachable,
// so it is created here too and failure to create it is ignored.
//
// It does three things past starting sshd. PowerShell becomes the SSH default
// shell, so a command sent to the guest needs one layer of quoting instead of
// cmd.exe's three. The generated public key goes into
// administrators_authorized_keys - the file sshd's stock config reads for
// members of the administrators group - so a Windows golden is key-based like
// the Linux ones and plain ssh and scp work from the generated ssh-config
// without a password prompt. And that file's ACL is reset to Administrators
// and SYSTEM: sshd ignores it, silently, if anyone else can write it.
func windowsPostInstall(pubKey string) string {
	return `powershell -ExecutionPolicy Bypass -NoProfile -Command ` +
		`"Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0; ` +
		`Set-Service -Name sshd -StartupType Automatic; ` +
		`Start-Service sshd; ` +
		`New-NetFirewallRule -Name sshd -DisplayName 'OpenSSH Server' -Enabled True ` +
		`-Direction Inbound -Protocol TCP -Action Allow -LocalPort 22 -ErrorAction SilentlyContinue; ` +
		`$k = 'HKLM:\SOFTWARE\OpenSSH'; ` +
		`if (-not (Test-Path $k)) { New-Item -Path $k -Force }; ` +
		`New-ItemProperty -Path $k -Name DefaultShell -Value '` + windowsDefaultShell + `' -PropertyType String -Force; ` +
		`$d = Join-Path $env:ProgramData ssh; ` +
		`New-Item -Path $d -ItemType Directory -Force; ` +
		`$a = Join-Path $d administrators_authorized_keys; ` +
		`Set-Content -Path $a -Value '` + pubKey + `'; ` +
		`icacls $a /inheritance:r /grant Administrators:F /grant SYSTEM:F"`
}

// windowsDefaultShell is what sshd hands an exec request to once the
// post-install has pointed it there. Windows PowerShell rather than pwsh: it
// is in the box, and installing anything else would be another thing to go
// wrong an hour into an install.
const windowsDefaultShell = `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`

// windowsShutdown is the Windows spelling of `shutdown -h now`.
const windowsShutdown = "shutdown /s /t 0"

// nt5PostInstall is the whole post-install for guests that cannot run an SSH
// server: the machine shutting itself down IS the completion signal. XP-era
// shutdown.exe takes dashes, and the flags are batch-safe.
const nt5PostInstall = "shutdown -s -f -t 0"

// isNT5 marks the Windows generation with no AHCI drivers and no PowerShell:
// it installs from an IDE disk and builds a credless golden.
func isNT5(ostype string) bool {
	return strings.HasPrefix(ostype, "WindowsXP") || strings.HasPrefix(ostype, "Windows2000")
}

// isWindowsGuest matches both spellings of a guest type: the id a recipe and
// createvm use (Windows10_64) and the description showvminfo reports
// (Windows 10 (64-bit)).
func isWindowsGuest(ostype string) bool {
	return strings.HasPrefix(ostype, "Windows")
}

// GuestIsWindows reports whether an env's guest runs Windows, which decides
// how a command's argv has to be quoted for its shell. The VM's own guest
// type is the only answer available: golden records store no OS, a promoted
// golden has no recipe at all, and a derived recipe's ostype says nothing
// about its base - it defaults to Linux_64 even when that base is Windows.
func (e *Engine) GuestIsWindows(envName string) (bool, error) {
	env := e.St.Envs[envName]
	if env == nil {
		return false, fmt.Errorf("no env %q", envName)
	}
	ostype, err := e.VB.OSType(env.VMName)
	if err != nil {
		return false, err
	}
	return isWindowsGuest(ostype), nil
}

// buildWindowsGolden installs Windows from an ISO. Unlike the cloud image
// paths there is nothing to import: VirtualBox drives Windows setup through
// its own generated answer file, which takes tens of minutes. That cost is
// paid once - forks of the resulting golden boot at normal speed.
func (e *Engine) buildWindowsGolden(r recipe.Recipe, image, vmName, isoPath, goldensDir string, cpus, memMB int, progress func(string)) (*Golden, error) {
	// Checked before the install, not when the golden is recorded: a bad user
	// name should not cost forty minutes to find out about.
	if !validSSHUser(r.User) {
		return nil, fmt.Errorf("recipe %s: ssh user contains an illegal character", image)
	}
	// Same reasoning, both cost tens of minutes to discover at install time:
	// XP-era setup halts at the key screen without a key, and it has no SSH
	// server for the default post-install to reach, so the readiness wait
	// would time out. A recipe that says WindowsXP must also say ssh: false.
	if isNT5(r.OSType) {
		if r.Key == "" {
			return nil, fmt.Errorf("recipe %s: this Windows generation cannot install without a product key.\ncopy the recipe into %%LOCALAPPDATA%%\\terrarium\\recipes\\ and add yours as key:", image)
		}
		if r.UseSSH() {
			return nil, fmt.Errorf("recipe %s: %s has no built-in SSH server; the recipe must set ssh: false", image, r.OSType)
		}
	}
	folder, err := e.VB.MachineFolder()
	if err != nil {
		return nil, err
	}
	if err := e.VB.CreateVM(vmName, r.OSType); err != nil {
		return nil, err
	}

	// From here the VM is registered, so failures leave it for inspection.
	g, err := e.installWindows(r, image, vmName, isoPath, goldensDir, filepath.Join(folder, vmName), cpus, memMB, progress)
	if err != nil {
		return nil, leftRegistered(vmName, err)
	}
	return g, nil
}

func (e *Engine) installWindows(r recipe.Recipe, image, vmName, isoPath, goldensDir, vmFolder string, cpus, memMB int, progress func(string)) (*Golden, error) {
	if err := e.VB.SetFirmwareTPM(vmName, r.UseEFI(), r.UseTPM()); err != nil {
		return nil, err
	}
	if err := e.VB.ModifyCPUMem(vmName, cpus, memMB); err != nil {
		return nil, err
	}

	vdiPath := filepath.Join(vmFolder, image+".vdi")
	if err := e.VB.CreateDynamicDisk(vdiPath, r.DiskGB*1024); err != nil {
		return nil, err
	}
	// XP-era Windows has no AHCI driver and bluescreens 0x7B booting from a
	// SATA disk, so it gets IDE. The controller is named for its bus so the
	// storage list reads honestly.
	diskCtrl := diskController
	if isNT5(r.OSType) {
		diskCtrl = "IDE"
		if err := e.VB.AddIDEController(vmName, diskCtrl); err != nil {
			return nil, err
		}
	} else {
		if err := e.VB.AddSATAController(vmName, diskCtrl, 2); err != nil {
			return nil, err
		}
	}
	if err := e.VB.AttachHDD(vmName, diskCtrl, 0, vdiPath); err != nil {
		return nil, err
	}

	var port int
	if r.UseSSH() {
		var err error
		if port, err = e.freePort(); err != nil {
			return nil, err
		}
		if err := e.VB.SetNATSSH(vmName, port); err != nil {
			return nil, err
		}
	}

	// A guest with no SSH still needs a completion signal, so its whole
	// post-install is a shutdown: setup finishing turns the machine off, and
	// there is nothing to install a key for.
	postCmd := nt5PostInstall
	var keyPath string
	if r.UseSSH() {
		keyPath = seed.KeyPath(goldensDir, image)
		if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
			return nil, err
		}
		// The same generator the cloud images use; the post-install installs
		// the public half where cloud-init would have.
		pubKey, err := sshx.EnsureKey(keyPath, r.User)
		if err != nil {
			return nil, err
		}
		postCmd = windowsPostInstall(pubKey)
	}

	progress(fmt.Sprintf("unattended install, up to %d min (once per image; forks are seconds)", r.InstallTimeoutMin))
	err := e.VB.UnattendedInstall(vbox.UnattendedOpts{
		VM:               vmName,
		ISO:              isoPath,
		User:             r.User,
		Password:         r.Password,
		FullName:         r.User,
		Key:              r.Key,
		ImageIndex:       r.ImageIndex,
		PostInstallCmd:   postCmd,
		InstallAdditions: r.UseAdditions(),
		StartVM:          true,
	})
	if err != nil {
		return nil, err
	}

	if r.UseSSH() {
		// No cloud-init here: sshd answering is the whole readiness signal, and
		// it only answers once setup, first logon and the post-install script
		// are done.
		if err := sshx.WaitReady(port, time.Duration(r.InstallTimeoutMin)*time.Minute); err != nil {
			return nil, fmt.Errorf("%w\nthe install may have stalled: check the console, or C:\\vboxpostinstall.log in the guest", err)
		}
		progress("install finished, SSH is up")
		if err := e.shutdownGuest(vmName, port, r.User, r.Password, keyPath, windowsShutdown, progress); err != nil {
			return nil, err
		}
	} else {
		progress("waiting for the installer to shut the machine down")
		if err := e.VB.WaitOff(vmName, time.Duration(r.InstallTimeoutMin)*time.Minute); err != nil {
			return nil, fmt.Errorf("%w\nthe install may have stalled: `terrarium screenshot` the golden's console to see where", err)
		}
		progress("install finished, machine powered itself off")
	}
	if err := e.ejectInstallMedia(vmName, vdiPath, progress); err != nil {
		return nil, err
	}

	g := &Golden{VMName: vmName}
	if r.UseSSH() {
		g.SSHUser = r.User
		g.SSHKey = keyPath
		// The password stays on the record beside the key: RDP authenticates
		// with it, and it is what unlocks the console.
		g.SSHPassword = r.Password
		g.Shell = ShellPowerShell
	}
	return e.recordGolden(image, g, progress)
}

// diskExts are the media that must never be detached as if they were a DVD:
// doing so would swap the golden's boot disk for an empty drive.
var diskExts = map[string]bool{".vdi": true, ".vmdk": true, ".vhd": true, ".vhdx": true, ".hdd": true}

// ejectInstallMedia takes the installation media and the answer-file floppy
// back off the VM. VirtualBox leaves them attached - its own post-install
// template carries a "@todo eject DVD install media" where the eject should be
// - and a golden that boots the installer again on every fork is no golden.
//
// Everything that is not the boot disk goes, rather than only files ending
// .iso: the auxiliary medium VirtualBox builds for the answer file does not
// reliably have that extension, and anything left behind re-runs the installer.
func (e *Engine) ejectInstallMedia(vmName, bootDisk string, progress func(string)) error {
	atts, err := e.VB.StorageAttachments(vmName)
	if err != nil {
		return err
	}
	for _, a := range atts {
		if strings.EqualFold(a.Medium, bootDisk) || diskExts[strings.ToLower(filepath.Ext(a.Medium))] {
			continue
		}
		progress("ejecting " + filepath.Base(a.Medium))
		if err := e.VB.DetachDVD(vmName, a.Controller, a.Port, a.Device); err != nil {
			return err
		}
	}

	ctrls, err := e.VB.StorageControllers(vmName)
	if err != nil {
		return err
	}
	for _, c := range ctrls {
		if strings.EqualFold(c.Bus, "Floppy") {
			progress("removing the unattended floppy")
			if err := e.VB.RemoveStorageController(vmName, c.Name); err != nil {
				return err
			}
		}
	}
	return nil
}
