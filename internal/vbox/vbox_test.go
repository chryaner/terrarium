package vbox

import (
	"errors"
	"strings"
	"testing"
)

// The lock retry exists because VirtualBox holds a session lock for seconds
// after a VM stops. Matching too little causes spurious failures; matching too
// much retries things that will never succeed.
func TestIsLockErr(t *testing.T) {
	for _, s := range []string{
		"VBoxManage snapshot: exit status 1\nE_ACCESSDENIED",
		"rc=E_ACCESSDENIED, LockMachine(a->session, LockType_Shared)",
		"VBOX_E_INVALID_OBJECT_STATE",
		"The machine 'trr-demo' is already locked by a session",
	} {
		if !isLockErr(errors.New(s)) {
			t.Errorf("should be retried as a lock error: %q", s)
		}
	}
	for _, s := range []string{
		"Could not find a registered machine named 'trr-gone'",
		"VBoxManage: error: Failed to open image",
		"",
	} {
		if isLockErr(errors.New(s)) {
			t.Errorf("should not be retried as a lock error: %q", s)
		}
	}
}

func TestIsNotRegistered(t *testing.T) {
	if !IsNotRegistered(errors.New(`VBoxManage: error: Could not find a registered machine named 'trr-gone'`)) {
		t.Error("the standard wording should be recognised")
	}
	// VirtualBox is not consistent about the leading capital.
	if !IsNotRegistered(errors.New("could not find a registered machine with UUID {...}")) {
		t.Error("matching should not depend on case")
	}
	if IsNotRegistered(errors.New("E_ACCESSDENIED")) {
		t.Error("a lock error is not a missing machine")
	}
	if IsNotRegistered(nil) {
		t.Error("nil is not an error")
	}
}

// The engine pastes this into an error message, so it has to be runnable.
func TestManualDeleteHint(t *testing.T) {
	if got := ManualDeleteHint("trr-golden-win11"); got != "VBoxManage unregistervm trr-golden-win11 --delete-all" {
		t.Errorf("got %q", got)
	}
}

// After an unattended install VirtualBox leaves the install ISO, the guest
// additions ISO and the answer-file floppy attached.
const windowsInstallInfo = `name="trr-golden-win11"` + "\r\n" +
	`storagecontrollername0="SATA"` + "\r\n" +
	`storagecontrollertype0="IntelAhci"` + "\r\n" +
	`storagecontrollerbustype0="SATA"` + "\r\n" +
	`storagecontrollername1="Floppy"` + "\r\n" +
	`storagecontrollertype1="I82078"` + "\r\n" +
	`storagecontrollerbustype1="Floppy"` + "\r\n" +
	`"SATA-0-0"="C:\Users\dev\VirtualBox VMs\trr-golden-win11\win11.vdi"` + "\r\n" +
	`"SATA-ImageUUID-0-0"="8b2c1f0e-1111-2222-3333-444455556666"` + "\r\n" +
	`"SATA-hot-pluggable-0-0"="off"` + "\r\n" +
	`"SATA-nonrotational-0-0"="off"` + "\r\n" +
	`"SATA-discard-0-0"="off"` + "\r\n" +
	`"SATA-1-0"="C:\isos\Win11_Eval.iso"` + "\r\n" +
	`"SATA-ImageUUID-1-0"="9c3d2e1f-aaaa-bbbb-cccc-ddddeeeeffff"` + "\r\n" +
	`"SATA-tempeject-1-0"="off"` + "\r\n" +
	`"SATA-IsEjected-1-0"="off"` + "\r\n" +
	`"SATA-hot-pluggable-1-0"="off"` + "\r\n" +
	`"SATA-2-0"="C:\Program Files\Oracle\VirtualBox\VBoxGuestAdditions.iso"` + "\r\n" +
	`"Floppy-0-0"="C:\Users\dev\VirtualBox VMs\trr-golden-win11\unattended.vfd"` + "\r\n" +
	`"Floppy-1-0"="emptydrive"` + "\r\n" +
	`VMState="poweroff"` + "\r\n"

func TestParseStorageAttachments(t *testing.T) {
	atts := parseStorageAttachments(windowsInstallInfo)

	want := []StorageAttachment{
		{Controller: "SATA", Port: 0, Device: 0, Medium: `C:\Users\dev\VirtualBox VMs\trr-golden-win11\win11.vdi`},
		{Controller: "SATA", Port: 1, Device: 0, Medium: `C:\isos\Win11_Eval.iso`},
		{Controller: "SATA", Port: 2, Device: 0, Medium: `C:\Program Files\Oracle\VirtualBox\VBoxGuestAdditions.iso`},
		{Controller: "Floppy", Port: 0, Device: 0, Medium: `C:\Users\dev\VirtualBox VMs\trr-golden-win11\unattended.vfd`},
	}
	if len(atts) != len(want) {
		t.Fatalf("expected %d attachments, got %d: %+v", len(want), len(atts), atts)
	}
	for i, w := range want {
		if atts[i] != w {
			t.Errorf("attachment %d: got %+v, want %+v", i, atts[i], w)
		}
	}
}

// ImageUUID, hot-pluggable, nonrotational, discard, tempeject and IsEjected
// lines repeat the key shape but describe the slot, not another medium. A real
// XP install where these were mistaken for attachments made eject try to
// detach a controller named "SATA-hot-pluggable".
func TestParseStorageAttachmentsSkipsMetadata(t *testing.T) {
	for _, a := range parseStorageAttachments(windowsInstallInfo) {
		for _, meta := range []string{"ImageUUID", "hot-pluggable", "nonrotational", "discard", "tempeject", "IsEjected"} {
			if strings.Contains(a.Controller, meta) {
				t.Errorf("metadata line parsed as an attachment: %+v", a)
			}
		}
	}
}

func TestParseStorageAttachmentsSkipsEmptySlots(t *testing.T) {
	out := `"IDE-0-0"="none"` + "\r\n" +
		`"IDE-1-0"="emptydrive"` + "\r\n" +
		`"IDE-1-1"=""` + "\r\n"

	if atts := parseStorageAttachments(out); len(atts) != 0 {
		t.Errorf("empty slots are not attachments, got %+v", atts)
	}
}

func TestUnattendedArgs(t *testing.T) {
	args := unattendedArgs(UnattendedOpts{
		VM:               "trr-golden-win11",
		ISO:              `C:\isos\win11.iso`,
		User:             "terrarium",
		Password:         "Terrarium1!",
		FullName:         "terrarium",
		Key:              "AAAAA-BBBBB-CCCCC-DDDDD-EEEEE",
		ImageIndex:       3,
		PostInstallCmd:   `powershell -Command "whoami"`,
		InstallAdditions: true,
		StartVM:          true,
	})

	want := []string{
		"unattended", "install", "trr-golden-win11",
		"--iso", `C:\isos\win11.iso`,
		"--user", "terrarium",
		"--user-password", "Terrarium1!",
		"--full-user-name", "terrarium",
		"--key", "AAAAA-BBBBB-CCCCC-DDDDD-EEEEE",
		"--image-index", "3",
		"--post-install-command", `powershell -Command "whoami"`,
		"--install-additions",
		"--start-vm=headless",
	}
	if len(args) != len(want) {
		t.Fatalf("got %d args, want %d:\n%v", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("arg %d: got %q, want %q", i, args[i], want[i])
		}
	}
}

// Empty options must not become empty flag values: `--key ""` is not the same
// as no key, and evaluation ISOs need no key at all.
func TestUnattendedArgsOmitsEmpties(t *testing.T) {
	args := unattendedArgs(UnattendedOpts{
		VM:       "vm",
		ISO:      "a.iso",
		User:     "terrarium",
		Password: "pw",
	})

	want := []string{"unattended", "install", "vm", "--iso", "a.iso",
		"--user", "terrarium", "--user-password", "pw"}
	if len(args) != len(want) {
		t.Fatalf("got %v, want %v", args, want)
	}
	for _, flag := range []string{"--key", "--image-index", "--full-user-name",
		"--post-install-command", "--install-additions", "--start-vm=headless"} {
		for _, a := range args {
			if a == flag {
				t.Errorf("%s should be omitted when unset: %v", flag, args)
			}
		}
	}
}

// image-index 0 means "not specified": VirtualBox numbers editions from 1.
func TestUnattendedArgsSkipsZeroImageIndex(t *testing.T) {
	args := unattendedArgs(UnattendedOpts{VM: "vm", ISO: "a.iso", ImageIndex: 0})
	for _, a := range args {
		if a == "--image-index" {
			t.Errorf("index 0 must be omitted: %v", args)
		}
	}
}

func TestParseVMList(t *testing.T) {
	out := "\"suse15\" {4402c3a8-7cae-48ec-aabf-0f6c03e76b98}\r\n" +
		"\"centos 7\" {5ca8617a-939c-4665-8446-d2f6d972fc29}\r\n"

	vms := parseVMList(out)
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs, got %d", len(vms))
	}
	if vms[0].Name != "suse15" || vms[0].UUID != "4402c3a8-7cae-48ec-aabf-0f6c03e76b98" {
		t.Errorf("unexpected first VM: %+v", vms[0])
	}
	if vms[1].Name != "centos 7" {
		t.Errorf("names with spaces must parse, got %q", vms[1].Name)
	}
}

func TestParseVMListEmpty(t *testing.T) {
	if vms := parseVMList(""); len(vms) != 0 {
		t.Errorf("expected no VMs, got %d", len(vms))
	}
}

func TestParseMachineFolder(t *testing.T) {
	out := "API version:                     7_2\r\n" +
		"Default machine folder:          C:\\Users\\dev\\VirtualBox VMs\r\n" +
		"Raw-mode Supported:              no\r\n"

	folder, err := parseMachineFolder(out)
	if err != nil {
		t.Fatal(err)
	}
	if folder != `C:\Users\dev\VirtualBox VMs` {
		t.Errorf("unexpected folder: %q", folder)
	}
}

func TestParseMachineFolderMissing(t *testing.T) {
	if _, err := parseMachineFolder("nothing here"); err == nil {
		t.Error("expected an error when the folder line is absent")
	}
}

func TestParseMachineReadable(t *testing.T) {
	out := "VMState=\"poweroff\"\r\nmemory=8192\r\nnot a kv line\r\n"
	kv := parseMachineReadable(out)
	if kv["VMState"] != "poweroff" {
		t.Errorf("VMState: got %q", kv["VMState"])
	}
	if kv["memory"] != "8192" {
		t.Errorf("memory: got %q", kv["memory"])
	}
}

// Layout of an imported Ubuntu cloud OVA, as reported by VirtualBox 7.2:
// the disk on SCSI, plus an IDE and a floppy controller the appliance ships
// and the guest stalls on.
const ubuntuOVAInfo = `name="trr-golden-ubuntu-24.04"` + "\r\n" +
	`ostype="Ubuntu (64-bit)"` + "\r\n" +
	`memory=2048` + "\r\n" +
	`storagecontrollername0="IDE"` + "\r\n" +
	`storagecontrollertype0="PIIX4"` + "\r\n" +
	`storagecontrollerbustype0="IDE"` + "\r\n" +
	`storagecontrollerinstance0="0"` + "\r\n" +
	`storagecontrollermaxportcount0="2"` + "\r\n" +
	`storagecontrollerportcount0="2"` + "\r\n" +
	`storagecontrollerbootable0="on"` + "\r\n" +
	`storagecontrollername1="SCSI"` + "\r\n" +
	`storagecontrollertype1="LsiLogic"` + "\r\n" +
	`storagecontrollerbustype1="SCSI"` + "\r\n" +
	`storagecontrollerinstance1="0"` + "\r\n" +
	`storagecontrollermaxportcount1="16"` + "\r\n" +
	`storagecontrollerportcount1="16"` + "\r\n" +
	`storagecontrollerbootable1="on"` + "\r\n" +
	`storagecontrollername2="Floppy"` + "\r\n" +
	`storagecontrollertype2="I82078"` + "\r\n" +
	`storagecontrollerbustype2="Floppy"` + "\r\n" +
	`storagecontrollerinstance2="0"` + "\r\n" +
	`storagecontrollermaxportcount2="1"` + "\r\n" +
	`storagecontrollerportcount2="1"` + "\r\n" +
	`storagecontrollerbootable2="off"` + "\r\n" +
	`"SCSI-0-0"="C:\Users\dev\VirtualBox VMs\trr-golden-ubuntu-24.04\disk.vmdk"` + "\r\n" +
	`VMState="poweroff"` + "\r\n"

func TestParseStorageControllers(t *testing.T) {
	ctrls := parseStorageControllers(ubuntuOVAInfo)
	if len(ctrls) != 3 {
		t.Fatalf("expected 3 controllers, got %d: %+v", len(ctrls), ctrls)
	}
	want := []StorageController{
		{Name: "IDE", Bus: "IDE"},
		{Name: "SCSI", Bus: "SCSI"},
		{Name: "Floppy", Bus: "Floppy"},
	}
	for i, w := range want {
		if ctrls[i] != w {
			t.Errorf("controller %d: got %+v, want %+v", i, ctrls[i], w)
		}
	}
}

// Older VirtualBox reports the controller type but no bus type. The floppy
// case matters: `get` finds the appliance's phantom floppy by bus and rips it
// out before boot.
func TestParseStorageControllersWithoutBusType(t *testing.T) {
	out := `storagecontrollername0="IDE"` + "\r\n" +
		`storagecontrollertype0="PIIX4"` + "\r\n" +
		`storagecontrollername1="SCSI"` + "\r\n" +
		`storagecontrollertype1="LsiLogic"` + "\r\n" +
		`storagecontrollername2="Floppy"` + "\r\n" +
		`storagecontrollertype2="I82078"` + "\r\n" +
		`storagecontrollername3="trrseed"` + "\r\n" +
		`storagecontrollertype3="IntelAhci"` + "\r\n"

	ctrls := parseStorageControllers(out)
	if len(ctrls) != 4 {
		t.Fatalf("expected 4 controllers, got %d: %+v", len(ctrls), ctrls)
	}
	for i, bus := range []string{"IDE", "SCSI", "Floppy", "SATA"} {
		if ctrls[i].Bus != bus {
			t.Errorf("controller %d (%s): bus %q, want %q", i, ctrls[i].Name, ctrls[i].Bus, bus)
		}
	}
}

// An unknown controller type still has to yield a usable bus: the name is the
// only hint left.
func TestControllerBusFromName(t *testing.T) {
	if bus := controllerBus("IDE Controller", "SomeFutureChipset"); bus != "IDE" {
		t.Errorf("got %q, want IDE", bus)
	}
	if bus := controllerBus("Mystery", ""); bus != "" {
		t.Errorf("got %q, want an empty bus", bus)
	}
}

func TestParseStorageControllersNone(t *testing.T) {
	if ctrls := parseStorageControllers("VMState=\"poweroff\"\r\n"); len(ctrls) != 0 {
		t.Errorf("expected no controllers, got %+v", ctrls)
	}
}

// The forward is one comma-separated argument; the empty field between host
// port and guest port is the guest IP, which NAT fills in.
func TestNATForwardRule(t *testing.T) {
	if got := natForwardRule("rdp", 42210, 3389); got != "rdp,tcp,127.0.0.1,42210,,3389" {
		t.Errorf("got %q", got)
	}
	// Same shape SetNATSSH has always produced, so the two stay comparable.
	if got := natForwardRule("ssh", 42200, 22); got != "ssh,tcp,127.0.0.1,42200,,22" {
		t.Errorf("got %q", got)
	}
}

func TestParseSnapshotNames(t *testing.T) {
	out := "SnapshotName=\"base\"\r\n" +
		"SnapshotUUID=\"aaa\"\r\n" +
		"SnapshotName-1=\"clean\"\r\n" +
		"CurrentSnapshotName=\"clean\"\r\n"

	names := parseSnapshotNames(out)
	if len(names) != 2 {
		t.Fatalf("expected 2 snapshot names, got %d: %v", len(names), names)
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	if !seen["base"] || !seen["clean"] {
		t.Errorf("missing names, got %v", names)
	}
}
