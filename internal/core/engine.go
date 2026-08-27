package core

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/chryaner/terrarium/internal/sshx"
	"github.com/chryaner/terrarium/internal/vbox"
)

// VMPrefix namespaces every VM terrarium creates.
const VMPrefix = "trr-"

// Version is what `terrarium --version` and MCP clients see. A var so
// releases can stamp it via -ldflags -X; source builds stay -dev.
var Version = "0.1.0-dev"

// DefaultExecTimeout bounds a single guest command, for the CLI and MCP alike.
const DefaultExecTimeout = 5 * time.Minute

const (
	DefaultSnapshot = "terrarium-base"
	cleanSnapshot   = "clean"
	bootTimeout     = 4 * time.Minute
	downTimeout     = 90 * time.Second
	// settleTime lets a credless guest get past the BIOS before its clean
	// snapshot is taken; there is no readiness signal to wait for instead.
	settleTime     = 15 * time.Second
	credlessNote   = "no SSH credentials recorded: interact via screenshot/keys"
	portRangeStart = 42200
	portRangeEnd   = 42300
)

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

type Engine struct {
	VB *vbox.Client
	St *State
}

func NewEngine() (*Engine, error) {
	vb, err := vbox.New()
	if err != nil {
		return nil, err
	}
	st, err := LoadState()
	if err != nil {
		return nil, err
	}
	return &Engine{VB: vb, St: st}, nil
}

func (e *Engine) findVM(name string) (*vbox.VM, error) {
	vms, err := e.VB.ListVMs()
	if err != nil {
		return nil, err
	}
	for i := range vms {
		if vms[i].Name == name {
			return &vms[i], nil
		}
	}
	return nil, fmt.Errorf("no VirtualBox VM named %q", name)
}

// Adopt records an existing VM+snapshot as a golden image. Re-running
// updates the record (e.g. to set SSH credentials later).
func (e *Engine) Adopt(vmName, snapshot, user, password, key string, takeSnapshot bool) (*Golden, error) {
	if !validSSHUser(user) {
		return nil, fmt.Errorf("ssh user contains an illegal character")
	}
	vm, err := e.findVM(vmName)
	if err != nil {
		return nil, err
	}
	if snapshot == "" {
		snapshot = DefaultSnapshot
	}
	has, err := e.VB.HasSnapshot(vmName, snapshot)
	if err != nil {
		return nil, err
	}
	if !has {
		if !takeSnapshot {
			return nil, fmt.Errorf("%s has no snapshot %q: pass --take-snapshot to create it, or --snapshot to use an existing one", vmName, snapshot)
		}
		if err := e.VB.TakeSnapshot(vmName, snapshot); err != nil {
			return nil, err
		}
	}
	g := e.St.Goldens[vmName]
	if g == nil {
		g = &Golden{}
		e.St.Goldens[vmName] = g
	}
	g.VMName = vmName
	g.UUID = vm.UUID
	g.Snapshot = snapshot
	if user != "" {
		g.SSHUser = user
	}
	if password != "" {
		g.SSHPassword = password
	}
	if key != "" {
		g.SSHKey = key
	}
	return g, e.St.Save()
}

func (e *Engine) freePort() (int, error) {
	used := map[int]bool{}
	for _, env := range e.St.Envs {
		used[env.SSHPort] = true
		used[env.RDPPort] = true
	}
	for p := portRangeStart; p < portRangeEnd; p++ {
		if used[p] {
			continue
		}
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err != nil {
			continue
		}
		l.Close()
		return p, nil
	}
	return 0, fmt.Errorf("no free port in %d-%d", portRangeStart, portRangeEnd)
}

// ForkOpts are per-env overrides. Zero values inherit the golden's hardware
// and share nothing.
type ForkOpts struct {
	CPUs          int
	MemMB         int
	ShareHostPath string
	// TTL, if set, marks the env for `terrarium gc` to remove once it elapses.
	TTL time.Duration
}

// Fork creates a disposable env from a golden: linked clone, NAT port
// forward, boot, wait for sshd, then an online snapshot so revert resumes
// from RAM in seconds instead of rebooting.
func (e *Engine) Fork(golden, name string, opts ForkOpts, progress func(string)) (*Env, error) {
	g := e.St.Goldens[golden]
	if g == nil {
		return nil, fmt.Errorf("no golden %q: run `terrarium adopt %s` first", golden, golden)
	}
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid env name %q (letters, digits, dashes)", name)
	}
	if e.St.Envs[name] != nil {
		return nil, fmt.Errorf("env %q already exists", name)
	}
	vmName := VMPrefix + name

	port, err := e.freePort()
	if err != nil {
		return nil, err
	}

	progress(fmt.Sprintf("cloning %s@%s -> %s", g.VMName, g.Snapshot, vmName))
	if err := e.VB.CloneLinked(g.VMName, g.Snapshot, vmName); err != nil {
		return nil, err
	}

	// Recorded before boot so `terrarium rm` can clean up a failed fork.
	env := &Env{
		VMName:  vmName,
		Golden:  golden,
		SSHPort: port,
		Created: time.Now(),
		Share:   opts.ShareHostPath,
	}
	if opts.TTL > 0 {
		env.Expires = env.Created.Add(opts.TTL)
	}
	if vm, err := e.findVM(vmName); err == nil {
		env.UUID = vm.UUID
	}
	e.St.Envs[name] = env
	if err := e.St.Save(); err != nil {
		return nil, err
	}

	if err := e.VB.SetNATSSH(vmName, port); err != nil {
		return env, err
	}
	if err := e.VB.ModifyCPUMem(vmName, opts.CPUs, opts.MemMB); err != nil {
		return env, err
	}
	// Forks inherit the golden's pointing hardware, PS/2-only on most images.
	// Absolute mouse injection (click) needs a USB tablet, and it has to land
	// before the clean snapshot so revert keeps the mouse.
	if err := e.VB.EnableMouseTablet(vmName); err != nil {
		return env, err
	}
	// Attached before the clean snapshot, so revert keeps the share.
	if opts.ShareHostPath != "" {
		if err := e.VB.SharedFolderAdd(vmName, ShareName, opts.ShareHostPath); err != nil {
			return env, err
		}
	}
	progress("booting")
	if err := e.VB.StartHeadless(vmName); err != nil {
		return env, err
	}
	if g.hasCreds() {
		if err := sshx.WaitReady(port, bootTimeout); err != nil {
			return env, err
		}
	} else {
		progress(credlessNote)
		// Nothing to wait for, but snapshotting the instant the VM starts
		// would leave the revert target at the BIOS. Give it some boot.
		time.Sleep(settleTime)
	}
	progress("snapshotting clean state (revert target)")
	if err := e.VB.TakeSnapshot(vmName, cleanSnapshot); err != nil {
		return env, err
	}
	return env, nil
}

// Up brings a project's env up: starts it if it exists, forks it if it does
// not. The bool reports whether the env was already there, since hardware
// changes in terrarium.yaml are not applied to an existing env.
func (e *Engine) Up(p *Project, progress func(string)) (*Env, bool, error) {
	if e.St.Envs[p.Name] != nil {
		env, err := e.Start(p.Name, progress)
		return env, true, err
	}

	if e.St.Goldens[p.Image] == nil {
		return nil, false, fmt.Errorf("no golden %q: run `terrarium get %s`, or `terrarium adopt` an existing VM under that name", p.Image, p.Image)
	}
	share, err := p.HostShare()
	if err != nil {
		return nil, false, err
	}
	opts := ForkOpts{CPUs: p.CPUs, MemMB: p.Memory, ShareHostPath: share}
	env, err := e.Fork(p.Image, p.Name, opts, progress)
	return env, false, err
}

// Start boots an existing env and waits for sshd. An env that is already
// running is left alone.
func (e *Engine) Start(name string, progress func(string)) (*Env, error) {
	env := e.St.Envs[name]
	if env == nil {
		return nil, fmt.Errorf("no env %q", name)
	}
	state, err := e.VB.VMState(env.VMName)
	if err != nil {
		return env, err
	}
	if state != "running" {
		progress("starting " + env.VMName)
		if err := e.VB.StartHeadless(env.VMName); err != nil {
			return env, err
		}
	}
	return env, e.waitReady(env, progress)
}

// waitReady blocks until sshd answers. An env forked from a credless golden
// has no sshd to wait for and returns at once - it is driven through the
// console instead.
func (e *Engine) waitReady(env *Env, progress func(string)) error {
	if !e.St.Goldens[env.Golden].hasCreds() {
		progress(credlessNote)
		return nil
	}
	return sshx.WaitReady(env.SSHPort, bootTimeout)
}

// Down shuts an env down without touching state, so `up` can start it again.
// ACPI first so the guest flushes its disks; a guest that ignores it (or has
// no ACPI handler) gets pulled after downTimeout.
func (e *Engine) Down(name string) error {
	env := e.St.Envs[name]
	if env == nil {
		return fmt.Errorf("no env %q", name)
	}
	state, err := e.VB.VMState(env.VMName)
	if err != nil {
		return err
	}
	if state != "running" {
		return nil
	}
	if err := e.VB.AcpiPowerButton(env.VMName); err != nil {
		return err
	}
	if err := e.VB.WaitOff(env.VMName, downTimeout); err != nil {
		if err := e.VB.PowerOff(env.VMName); err != nil {
			return err
		}
		return e.VB.WaitOff(env.VMName, 30*time.Second)
	}
	return nil
}

// Revert returns an env to its clean snapshot. The snapshot holds RAM state,
// so the machine resumes instead of booting.
func (e *Engine) Revert(name string, progress func(string)) error {
	env := e.St.Envs[name]
	if env == nil {
		return fmt.Errorf("no env %q", name)
	}
	progress("powering off")
	if err := e.VB.PowerOff(env.VMName); err != nil {
		return err
	}
	if err := e.VB.WaitOff(env.VMName, 30*time.Second); err != nil {
		return err
	}
	progress("restoring clean state")
	// By name, not RestoreCurrent: taking a named snapshot makes that one
	// current, so "current" would rewind to the user's last `terrarium
	// snapshot` rather than the clean state this promises.
	if err := e.VB.SnapshotRestore(env.VMName, cleanSnapshot); err != nil {
		return err
	}
	if err := e.VB.StartHeadless(env.VMName); err != nil {
		return err
	}
	return e.waitReady(env, progress)
}

func (e *Engine) Remove(name string) error {
	env := e.St.Envs[name]
	if env == nil {
		return fmt.Errorf("no env %q", name)
	}
	// A VM someone deleted outside terrarium is already in the state we are
	// trying to reach. Without this the record is unremovable, because every
	// call below keeps failing the same way.
	gone := false
	if err := e.VB.PowerOff(env.VMName); err != nil {
		if !vbox.IsNotRegistered(err) {
			return err
		}
		gone = true
	}
	if !gone {
		if err := e.VB.WaitOff(env.VMName, 30*time.Second); err != nil {
			return err
		}
		if err := e.VB.Unregister(env.VMName); err != nil && !vbox.IsNotRegistered(err) {
			return err
		}
	}
	removeRDPFile(name)
	// Only clear the shared TERMSRV entry if this env is the one that put it
	// there. An env that never used RDP does not own it, and it may well be
	// the user's own credential for something else on loopback.
	if env.RDPPort != 0 && !e.otherEnvHasRDP(name) {
		deleteRDPCredential(termsrvTarget(rdpHost))
	}
	delete(e.St.Envs, name)
	return e.St.Save()
}

// GCRemoval is one env gc would remove, and why.
type GCRemoval struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// gcRemovals decides which envs are collectable, sorted by name so the output
// is stable. Split out from GC so the decision can be tested without a VM: an
// env is dangling if its VM is gone, or expired if its TTL has passed. An env
// with no TTL is never collected on age.
func gcRemovals(envs map[string]*Env, exists map[string]bool, now time.Time) []GCRemoval {
	var out []GCRemoval
	for name, env := range envs {
		switch {
		case !exists[env.VMName]:
			out = append(out, GCRemoval{Name: name, Reason: "dangling (VM no longer in VirtualBox)"})
		case !env.Expires.IsZero() && !env.Expires.After(now):
			out = append(out, GCRemoval{Name: name, Reason: "expired"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// GC removes envs that have outlived their TTL and env records whose VM has
// vanished from VirtualBox. Envs without a TTL are left alone however old they
// are. dryRun returns the list without removing anything.
func (e *Engine) GC(dryRun bool, progress func(string)) ([]GCRemoval, error) {
	vms, err := e.VB.ListVMs()
	if err != nil {
		return nil, err
	}
	exists := make(map[string]bool, len(vms))
	for _, vm := range vms {
		exists[vm.Name] = true
	}
	removals := gcRemovals(e.St.Envs, exists, time.Now())
	if dryRun {
		return removals, nil
	}
	for _, r := range removals {
		progress(fmt.Sprintf("removing %s: %s", r.Name, r.Reason))
		if err := e.Remove(r.Name); err != nil {
			return removals, fmt.Errorf("removing %s: %w", r.Name, err)
		}
	}
	return removals, nil
}

// SSHTarget resolves connection details for an env from its golden's record.
func (e *Engine) SSHTarget(name string) (port int, user, password, key string, err error) {
	env := e.St.Envs[name]
	if env == nil {
		return 0, "", "", "", fmt.Errorf("no env %q", name)
	}
	g := e.St.Goldens[env.Golden]
	if g == nil || g.SSHUser == "" {
		return 0, "", "", "", fmt.Errorf("golden %q has no SSH user: re-run `terrarium adopt %s --user <user> [--password <pw> | --key <path>]`", env.Golden, env.Golden)
	}
	return env.SSHPort, g.SSHUser, g.SSHPassword, g.SSHKey, nil
}
