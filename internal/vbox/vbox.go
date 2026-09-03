// Package vbox is the only place terrarium talks to VirtualBox. Paths,
// argument syntax, output parsing, timeouts and version quirks stay here.
//
// Policy:
//   - every call has a hard timeout: a wedged VBoxSVC blocks even reads
//   - snapshots never use --live: it can hang mid-save and wedge the service
//   - lock errors are retried: VirtualBox session locks clear within seconds
//     of a VM powering off
package vbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultTimeout = 30 * time.Second
	slowTimeout    = 3 * time.Minute  // clone/snapshot/delete can be disk-heavy
	importTimeout  = 10 * time.Minute // OVA import extracts and converts the disk
	// A full clone rewrites the whole disk chain; a multi-GB golden on a slow
	// disk takes minutes where a linked clone takes a tenth of a second.
	fullCloneTimeout = 30 * time.Minute
)

type VM struct {
	Name    string `json:"name"`
	UUID    string `json:"uuid"`
	Running bool   `json:"running"`
}

type StorageController struct {
	Name string `json:"name"`
	Bus  string `json:"bus"`
}

type StorageAttachment struct {
	Controller string `json:"controller"`
	Port       int    `json:"port"`
	Device     int    `json:"device"`
	Medium     string `json:"medium"`
}

type Client struct {
	Exe     string
	Timeout time.Duration

	// The guest-type catalogue is read once and kept: see osTypeIDs.
	osTypesOnce sync.Once
	osTypes     map[string]string
	osTypesErr  error
}

func Locate() (string, error) {
	if p, err := exec.LookPath("VBoxManage"); err == nil {
		return p, nil
	}
	for _, p := range []string{
		`C:\Program Files\Oracle\VirtualBox\VBoxManage.exe`,
		`C:\Program Files (x86)\Oracle\VirtualBox\VBoxManage.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("VBoxManage not found: is VirtualBox installed?")
}

func New() (*Client, error) {
	exe, err := Locate()
	if err != nil {
		return nil, err
	}
	return &Client{Exe: exe, Timeout: DefaultTimeout}, nil
}

func (c *Client) run(args ...string) (string, error) {
	return c.runT(c.Timeout, args...)
}

func (c *Client) runT(timeout time.Duration, args ...string) (string, error) {
	out, err := c.runRaw(timeout, args...)
	if err != nil {
		return "", err
	}
	return out, nil
}

// runRaw hands back the output whether or not the command succeeded, for the
// callers that can still read something useful out of a failure. Everything
// else goes through runT, which drops the output of a failed call so it
// cannot be mistaken for a result.
func (c *Client) runRaw(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.Exe, args...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("VBoxManage %s timed out after %s (VBoxSVC may be wedged)", args[0], timeout)
	}
	if err != nil {
		return string(out), fmt.Errorf("VBoxManage %s: %w\n%s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func isLockErr(err error) bool {
	s := err.Error()
	return strings.Contains(s, "E_ACCESSDENIED") ||
		strings.Contains(s, "LockMachine") ||
		strings.Contains(s, "VBOX_E_INVALID_OBJECT_STATE") ||
		strings.Contains(s, "is already locked")
}

// ManualDeleteHint is the command a user runs to clear up a VM terrarium has
// deliberately left behind. It lives here because the exact syntax is the
// driver's business, not the engine's. discardstate comes first because
// unregistervm refuses a machine still holding a saved RAM image, and this
// hint is printed exactly when automatic cleanup failed - which includes the
// discard failing. The two are separated by `;`, not `&&`: discardstate errors
// on a machine that has no saved state, and the unregister must still run.
func ManualDeleteHint(vm string) string {
	return fmt.Sprintf("VBoxManage discardstate %s; VBoxManage unregistervm %s --delete-all", vm, vm)
}

// IsNotRegistered reports that VirtualBox has never heard of the VM - someone
// deleted it outside terrarium. Cleanup paths treat that as already done.
func IsNotRegistered(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "could not find a registered machine")
}

func (c *Client) retryLocked(attempts int, fn func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil || !isLockErr(err) {
			return err
		}
		time.Sleep(1500 * time.Millisecond)
	}
	return err
}

func (c *Client) Version() (string, error) {
	out, err := c.run("--version")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (c *Client) ListVMs() ([]VM, error) {
	all, err := c.run("list", "vms")
	if err != nil {
		return nil, err
	}
	running, err := c.run("list", "runningvms")
	if err != nil {
		return nil, err
	}
	vms := parseVMList(all)
	up := map[string]bool{}
	for _, vm := range parseVMList(running) {
		up[vm.UUID] = true
	}
	for i := range vms {
		vms[i].Running = up[vms[i].UUID]
	}
	return vms, nil
}

func (c *Client) MachineFolder() (string, error) {
	out, err := c.run("list", "systemproperties")
	if err != nil {
		return "", err
	}
	return parseMachineFolder(out)
}

func (c *Client) VMState(vm string) (string, error) {
	out, err := c.run("showvminfo", vm, "--machinereadable")
	if err != nil {
		return "", err
	}
	kv := parseMachineReadable(out)
	if s, ok := kv["VMState"]; ok {
		return s, nil
	}
	return "", fmt.Errorf("no VMState in showvminfo output for %s", vm)
}

// OSType is the guest type recorded for a VM. VirtualBox reports the
// description here ("Windows 10 (64-bit)"), not the id createvm takes
// (Windows10_64), and a clone inherits whatever its source had.
func (c *Client) OSType(vm string) (string, error) {
	out, err := c.run("showvminfo", vm, "--machinereadable")
	if err != nil {
		return "", err
	}
	kv := parseMachineReadable(out)
	if s, ok := kv["ostype"]; ok {
		return s, nil
	}
	return "", fmt.Errorf("no ostype in showvminfo output for %s", vm)
}

// CPUMem reports the hardware a VM is configured with, for `terrarium info`.
// A value VirtualBox does not report comes back as zero rather than an error:
// the rest of the report is still worth printing.
func (c *Client) CPUMem(vm string) (cpus, memMB int, err error) {
	out, err := c.run("showvminfo", vm, "--machinereadable")
	if err != nil {
		return 0, 0, err
	}
	kv := parseMachineReadable(out)
	cpus, _ = strconv.Atoi(kv["cpus"])
	memMB, _ = strconv.Atoi(kv["memory"])
	return cpus, memMB, nil
}

func (c *Client) HasSnapshot(vm, name string) (bool, error) {
	out, err := c.run("snapshot", vm, "list", "--machinereadable")
	if err != nil {
		if strings.Contains(err.Error(), "does not have any snapshots") {
			return false, nil
		}
		return false, err
	}
	for _, n := range parseSnapshotNames(out) {
		if n == name {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) TakeSnapshot(vm, name string) error {
	_, err := c.runT(slowTimeout, "snapshot", vm, "take", name)
	return err
}

func (c *Client) RestoreCurrent(vm string) error {
	return c.retryLocked(5, func() error {
		_, err := c.runT(slowTimeout, "snapshot", vm, "restorecurrent")
		return err
	})
}

// EnableMouseTablet gives a stopped VM an absolute pointing device, with the
// USB controller it rides on. Most images ship PS/2-only, which can only
// report relative motion; absolute mouse injection (PutMouseEvents) needs the
// tablet.
func (c *Client) EnableMouseTablet(vm string) error {
	_, err := c.run("modifyvm", vm, "--mouse", "usbtablet", "--usb-ohci", "on")
	return err
}

// CloneLinked keeps MACs: forks run in isolated NAT networks, and older
// guests pin interface config to the MAC address.
func (c *Client) CloneLinked(src, snapshot, dst string) error {
	_, err := c.runT(slowTimeout, "clonevm", src,
		"--snapshot", snapshot, "--options=link,keepallmacs",
		"--name", dst, "--register")
	return err
}

// CloneFull flattens a VM's current state - base disk plus every differencing
// layer - into a standalone copy that depends on nothing. This is how a fork
// becomes a golden: the result survives whatever happens to the original.
// MACs are kept for the same reason CloneLinked keeps them.
func (c *Client) CloneFull(src, dst string) error {
	_, err := c.runT(fullCloneTimeout, "clonevm", src,
		"--options=keepallmacs", "--name", dst, "--register")
	return err
}

// ImportOVA registers an appliance as a new VM. Zero cpus or memory keeps
// what the vendor shipped, which is what `terrarium import` wants for an
// appliance nobody has re-sized; `get` always passes both.
func (c *Client) ImportOVA(ovaPath, vmName string, cpus, memMB int) error {
	_, err := c.runT(importTimeout, importArgs(ovaPath, vmName, cpus, memMB)...)
	return err
}

// importArgs is separate so the flag list can be tested: an import takes
// minutes to fail, which is a slow way to find out a flag was wrong.
func importArgs(ovaPath, vmName string, cpus, memMB int) []string {
	args := []string{"import", ovaPath, "--vsys", "0", "--vmname", vmName}
	if cpus > 0 {
		args = append(args, "--cpus", strconv.Itoa(cpus))
	}
	if memMB > 0 {
		args = append(args, "--memory", strconv.Itoa(memMB))
	}
	// --eula accept applies to the vsys 0 selected above it.
	return append(args, "--eula", "accept")
}

func (c *Client) StorageControllers(vm string) ([]StorageController, error) {
	out, err := c.run("showvminfo", vm, "--machinereadable")
	if err != nil {
		return nil, err
	}
	return parseStorageControllers(out), nil
}

func (c *Client) RemoveStorageController(vm, name string) error {
	_, err := c.run("storagectl", vm, "--name", name, "--remove")
	return err
}

// AddSATAController adds an AHCI controller of our own. Appliance-supplied
// IDE controllers are not usable for media we attach: the noble kernel
// freezes during the ATAPI probe on the PIIX4 the Ubuntu OVA ships, while the
// same medium probes cleanly on AHCI.
func (c *Client) AddSATAController(vm, name string, portCount int) error {
	_, err := c.run("storagectl", vm,
		"--name", name,
		"--add", "sata",
		"--controller", "IntelAhci",
		"--portcount", strconv.Itoa(portCount))
	return err
}

// AddIDEController is for guests too old to carry AHCI drivers: XP-era
// Windows bluescreens (0x7B) the moment setup boots from a SATA disk.
func (c *Client) AddIDEController(vm, name string) error {
	_, err := c.run("storagectl", vm,
		"--name", name,
		"--add", "ide",
		"--controller", "PIIX4")
	return err
}

// CreateVM registers an empty VM. ostype only picks VirtualBox's defaults for
// the guest; it does not have to describe it exactly.
func (c *Client) CreateVM(name, ostype string) error {
	_, err := c.run("createvm", "--name", name, "--ostype", ostype, "--register")
	return err
}

// CloneMedium converts a disk image to VDI. Most distributions publish qcow2
// only, which VirtualBox cannot boot. Cloud images are mostly empty, so this
// runs in seconds even for multi-GB disks.
func (c *Client) CloneMedium(src, dst string) error {
	_, err := c.runT(slowTimeout, "clonemedium", "disk", src, dst, "--format", "VDI")
	return err
}

// CloseMedium drops a disk from VirtualBox's media registry without deleting
// the file. clonemedium registers its source, and a registered file gets in
// the way of the next run.
func (c *Client) CloseMedium(path string) error {
	_, err := c.run("closemedium", "disk", path)
	if err != nil && (strings.Contains(err.Error(), "could not be found") ||
		strings.Contains(err.Error(), "not found")) {
		return nil
	}
	return err
}

func (c *Client) AttachHDD(vm, controller string, port int, vdiPath string) error {
	_, err := c.run("storageattach", vm,
		"--storagectl", controller,
		"--port", strconv.Itoa(port),
		"--device", "0",
		"--type", "hdd",
		"--medium", vdiPath)
	return err
}

func (c *Client) AttachDVD(vm, controller string, port int, isoPath string) error {
	_, err := c.run("storageattach", vm,
		"--storagectl", controller,
		"--port", strconv.Itoa(port),
		"--device", "0",
		"--type", "dvddrive",
		"--medium", isoPath)
	return err
}

func (c *Client) DetachDVD(vm, controller string, port, device int) error {
	_, err := c.run("storageattach", vm,
		"--storagectl", controller,
		"--port", strconv.Itoa(port),
		"--device", strconv.Itoa(device),
		"--type", "dvddrive",
		"--medium", "emptydrive")
	return err
}

// CreateDynamicDisk makes an empty VDI that grows on demand, for guests we
// install from scratch rather than import.
func (c *Client) CreateDynamicDisk(path string, sizeMB int) error {
	_, err := c.runT(slowTimeout, "createmedium", "disk",
		"--filename", path,
		"--size", strconv.Itoa(sizeMB),
		"--format", "VDI")
	return err
}

// SetFirmwareTPM gives a VM the firmware and TPM a modern Windows expects.
func (c *Client) SetFirmwareTPM(vm string, efi, tpm bool) error {
	args := []string{"modifyvm", vm}
	if efi {
		args = append(args, "--firmware", "efi")
	}
	if tpm {
		args = append(args, "--tpm-type", "2.0")
	}
	if len(args) == 2 {
		return nil
	}
	_, err := c.run(args...)
	return err
}

type UnattendedOpts struct {
	VM               string
	ISO              string
	User             string
	Password         string
	FullName         string
	Key              string
	PostInstallCmd   string
	ImageIndex       int
	InstallAdditions bool
	StartVM          bool
}

// UnattendedInstall hands VirtualBox an installation ISO and the answer file
// it generates from its own templates. The call returns once the guest is
// booted into the installer; the install runs for tens of minutes after that.
func (c *Client) UnattendedInstall(o UnattendedOpts) error {
	_, err := c.runT(slowTimeout, unattendedArgs(o)...)
	return err
}

// unattendedArgs is separate so the flag list can be tested: a wrong flag here
// costs a 40-minute install to discover.
func unattendedArgs(o UnattendedOpts) []string {
	args := []string{"unattended", "install", o.VM, "--iso", o.ISO}
	if o.User != "" {
		args = append(args, "--user", o.User)
	}
	if o.Password != "" {
		args = append(args, "--user-password", o.Password)
	}
	if o.FullName != "" {
		args = append(args, "--full-user-name", o.FullName)
	}
	if o.Key != "" {
		args = append(args, "--key", o.Key)
	}
	if o.ImageIndex > 0 {
		args = append(args, "--image-index", strconv.Itoa(o.ImageIndex))
	}
	if o.PostInstallCmd != "" {
		args = append(args, "--post-install-command", o.PostInstallCmd)
	}
	if o.InstallAdditions {
		args = append(args, "--install-additions")
	}
	if o.StartVM {
		args = append(args, "--start-vm=headless")
	}
	return args
}

// StorageAttachments lists the media currently attached to a VM.
func (c *Client) StorageAttachments(vm string) ([]StorageAttachment, error) {
	out, err := c.run("showvminfo", vm, "--machinereadable")
	if err != nil {
		return nil, err
	}
	return parseStorageAttachments(out), nil
}

// AcpiPowerButton asks the guest to shut down. Unlike PowerOff it gives the
// guest a chance to flush its disks; unlike a shutdown over SSH it works
// without credentials.
func (c *Client) AcpiPowerButton(vm string) error {
	_, err := c.run("controlvm", vm, "acpipowerbutton")
	return err
}

// SharedFolderAdd exposes a host folder to the VM, which must be powered off.
// No --automount: the guest mounts it from fstab instead, so the mount options
// are ours and the mount survives every boot rather than only the first.
func (c *Client) SharedFolderAdd(vm, name, hostPath string) error {
	_, err := c.run("sharedfolder", "add", vm, "--name", name, "--hostpath", hostPath)
	return err
}

// ModifyCPUMem overrides what the golden was built with. Zero means inherit.
func (c *Client) ModifyCPUMem(vm string, cpus, memMB int) error {
	args := []string{"modifyvm", vm}
	if cpus > 0 {
		args = append(args, "--cpus", strconv.Itoa(cpus))
	}
	if memMB > 0 {
		args = append(args, "--memory", strconv.Itoa(memMB))
	}
	if len(args) == 2 {
		return nil
	}
	_, err := c.run(args...)
	return err
}

// SetNATSSH puts nic1 on NAT with a host port forward to guest port 22.
func (c *Client) SetNATSSH(vm string, hostPort int) error {
	c.run("modifyvm", vm, "--nat-pf1", "delete", "ssh") // may not exist
	_, err := c.run("modifyvm", vm, "--nic1", "nat",
		"--nat-pf1", fmt.Sprintf("ssh,tcp,127.0.0.1,%d,,22", hostPort))
	return err
}

// StartSeparate boots the VM with a GUI window that can be closed without
// stopping it. VirtualBox can only attach a window at start, so an env already
// running headless has to be stopped before it can be shown.
func (c *Client) StartSeparate(vm string) error {
	_, err := c.runT(2*time.Minute, "startvm", vm, "--type", "separate")
	return err
}

// ScreenshotPNG captures the guest's screen. It works on any running VM,
// including one with no guest additions, no network and no SSH.
func (c *Client) ScreenshotPNG(vm, path string) error {
	_, err := c.run("controlvm", vm, "screenshotpng", path)
	return err
}

// KeyboardPutString types text into the guest. The whole string goes as one
// argument; splitting it would lose the spaces.
func (c *Client) KeyboardPutString(vm, text string) error {
	_, err := c.run("controlvm", vm, "keyboardputstring", text)
	return err
}

// KeyboardPutScancodes injects raw scancodes, for the keys with no printable
// form. See internal/keys.
func (c *Client) KeyboardPutScancodes(vm string, hexCodes []string) error {
	args := append([]string{"controlvm", vm, "keyboardputscancode"}, hexCodes...)
	_, err := c.run(args...)
	return err
}

// Mouse buttons, matching VirtualBox's MouseButtonState.
const (
	MouseLeft   = 0x01
	MouseRight  = 0x02
	MouseMiddle = 0x04
)

// MouseEvent is one absolute mouse state: where the pointer is and which
// buttons are held while it is there. Coordinates are 0-based screen pixels,
// exactly what ScreenshotPNG shows. Implemented in mouse_windows.go: mouse
// injection is the one thing VBoxManage has no verb for, so it is the one
// call that goes through VirtualBox's COM API instead.
type MouseEvent struct {
	X, Y    int
	Buttons int
}

// NATForwardRunning adds a port forward to a VM that is already up. modifyvm
// only works on a stopped VM, so this is the runtime equivalent.
func (c *Client) NATForwardRunning(vm, rule string, hostPort, guestPort int) error {
	c.run("controlvm", vm, "natpf1", "delete", rule) // may not exist
	_, err := c.run("controlvm", vm, "natpf1", natForwardRule(rule, hostPort, guestPort))
	return err
}

func natForwardRule(rule string, hostPort, guestPort int) string {
	return fmt.Sprintf("%s,tcp,127.0.0.1,%d,,%d", rule, hostPort, guestPort)
}

// SnapshotRestore rewinds to a named snapshot.
func (c *Client) SnapshotRestore(vm, snap string) error {
	return c.retryLocked(5, func() error {
		_, err := c.runT(slowTimeout, "snapshot", vm, "restore", snap)
		return err
	})
}

func (c *Client) StartHeadless(vm string) error {
	_, err := c.runT(2*time.Minute, "startvm", vm, "--type", "headless")
	return err
}

func (c *Client) PowerOff(vm string) error {
	_, err := c.run("controlvm", vm, "poweroff")
	if err != nil && (strings.Contains(err.Error(), "is not currently running") ||
		strings.Contains(err.Error(), "Invalid machine state")) {
		return nil
	}
	return err
}

// DiscardState throws away a machine's saved RAM image, leaving it powered off
// and mutable. A clone of an online snapshot arrives Saved, and VirtualBox
// refuses both modifyvm and unregistervm on a machine in that state. The guest
// cold-boots afterwards instead of resuming.
func (c *Client) DiscardState(vm string) error {
	return c.retryLocked(5, func() error {
		_, err := c.run("discardstate", vm)
		return err
	})
}

// WaitOff blocks until the VM is powered down, then briefly longer: the
// session lock outlives the state change and blocks snapshot/unregister.
func (c *Client) WaitOff(vm string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, err := c.VMState(vm)
		if err != nil {
			return err
		}
		switch state {
		case "poweroff", "aborted", "saved", "aborted-saved":
			time.Sleep(1500 * time.Millisecond)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("%s still not powered off after %s", vm, timeout)
}

func (c *Client) Unregister(vm string) error {
	return c.retryLocked(5, func() error {
		_, err := c.runT(slowTimeout, "unregistervm", vm, "--delete-all")
		return err
	})
}

var vmLineRe = regexp.MustCompile(`(?m)^"(.*)" \{([0-9a-fA-F-]+)\}\s*$`)

func parseVMList(out string) []VM {
	var vms []VM
	for _, m := range vmLineRe.FindAllStringSubmatch(out, -1) {
		vms = append(vms, VM{Name: m[1], UUID: m[2]})
	}
	return vms
}

func parseMachineFolder(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "Default machine folder:"); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("default machine folder not found in systemproperties output")
}

// parseMachineReadable parses `Key="value"` / `Key=value` lines.
func parseMachineReadable(out string) map[string]string {
	kv := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), "=")
		if !ok {
			continue
		}
		kv[k] = strings.Trim(v, `"`)
	}
	return kv
}

// busByType maps VirtualBox controller types to their bus, for the versions
// that report storagecontrollertype<N> but no storagecontrollerbustype<N>.
var busByType = map[string]string{
	"PIIX3":       "IDE",
	"PIIX4":       "IDE",
	"ICH6":        "IDE",
	"IntelAhci":   "SATA",
	"LsiLogic":    "SCSI",
	"BusLogic":    "SCSI",
	"LsiLogicSas": "SAS",
	"NVMe":        "NVMe",
	"USB":         "USB",
	"VirtIO":      "VirtIO",
	"I82078":      "Floppy",
}

var busNames = []string{"IDE", "SATA", "SCSI", "SAS", "NVMe", "USB", "Floppy"}

// parseStorageControllers reads the storagecontroller* keys, which are
// numbered from 0 with no count key to bound them.
func parseStorageControllers(out string) []StorageController {
	kv := parseMachineReadable(out)
	var ctrls []StorageController
	for i := 0; ; i++ {
		n := strconv.Itoa(i)
		name, ok := kv["storagecontrollername"+n]
		if !ok {
			return ctrls
		}
		bus := kv["storagecontrollerbustype"+n]
		if bus == "" {
			bus = controllerBus(name, kv["storagecontrollertype"+n])
		}
		ctrls = append(ctrls, StorageController{Name: name, Bus: bus})
	}
}

// Attachments appear as `"<controller>-<port>-<device>"="<medium>"`. The same
// shape is reused for per-slot metadata like
// `"<controller>-ImageUUID-<port>-<device>"`; parseStorageAttachments tells
// those from real media by their value.
var attachmentRe = regexp.MustCompile(`(?m)^"([^"]+)-(\d+)-(\d+)"="([^"]*)"\s*$`)

func parseStorageAttachments(out string) []StorageAttachment {
	var atts []StorageAttachment
	for _, m := range attachmentRe.FindAllStringSubmatch(out, -1) {
		name := m[1]
		medium := m[4]
		// The same key shape is reused for per-slot metadata:
		// "<ctrl>-ImageUUID-<port>-<device>", and in VirtualBox 7.2 also
		// "-hot-pluggable-", "-nonrotational-", "-discard-", "-tempeject-"
		// and "-IsEjected-". Their values are a UUID or on/off, never a path,
		// so a medium with no path separator is one of those, not a real
		// attachment. Empty and placeholder slots ("none", "emptydrive", "")
		// fail the same test.
		if !strings.ContainsAny(medium, `/\`) {
			continue
		}
		port, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		device, err := strconv.Atoi(m[3])
		if err != nil {
			continue
		}
		atts = append(atts, StorageAttachment{
			Controller: name,
			Port:       port,
			Device:     device,
			Medium:     medium,
		})
	}
	return atts
}

func controllerBus(name, ctype string) string {
	if bus, ok := busByType[ctype]; ok {
		return bus
	}
	upper := strings.ToUpper(name)
	for _, bus := range busNames {
		if strings.Contains(upper, strings.ToUpper(bus)) {
			return bus
		}
	}
	return ""
}

// parseSnapshotNames collects SnapshotName, SnapshotName-1, ... values.
func parseSnapshotNames(out string) []string {
	var names []string
	for k, v := range parseMachineReadable(out) {
		if strings.HasPrefix(k, "SnapshotName") {
			names = append(names, v)
		}
	}
	return names
}
