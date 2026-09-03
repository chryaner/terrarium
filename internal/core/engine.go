package core

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
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

// Adopt records an existing VM+snapshot as a golden image. Re-running updates
// the record, which is how credentials are set later: adopt credless, work out
// the login through the console, adopt again with what worked. image names the
// golden; empty means the VM's own name.
func (e *Engine) Adopt(vmName, image, snapshot, user, password, key, shell, transport string, takeSnapshot bool) (*Golden, error) {
	if !validSSHUser(user) {
		return nil, fmt.Errorf("ssh user contains an illegal character")
	}
	if shell != "" && !ValidShell(shell) {
		return nil, fmt.Errorf("unknown shell %q: one of %s", shell, strings.Join(Shells, ", "))
	}
	if transport != "" && !ValidTransport(transport) {
		return nil, fmt.Errorf("unknown transport %q: one of %s", transport, strings.Join(Transports, ", "))
	}
	// Guest Additions have no key auth and no way to ask for one, so a record
	// that cannot log in is worth refusing here rather than at the first exec.
	if transport == TransportGuestControl && user != "" && password == "" {
		return nil, fmt.Errorf("the guestcontrol transport authenticates with a password: pass --password as well as --user")
	}
	if image == "" {
		image = vmName
	} else if !goldenNameRe.MatchString(image) {
		return nil, fmt.Errorf("invalid golden name %q (letters, digits, dots, dashes)", image)
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
	g := e.St.Goldens[image]
	if g == nil {
		g = &Golden{}
		e.St.Goldens[image] = g
	}
	g.VMName = vmName
	g.UUID = vm.UUID
	g.Snapshot = snapshot
	// Probed now so `ls` and `info` can say what this thing is - the reason
	// adopt exists is that terrarium knows nothing else about the VM. A
	// VirtualBox that will not answer is not worth failing the adopt over.
	if ostype, err := e.VB.OSTypeID(vmName); err == nil {
		g.OSType = ostype
	}
	if user != "" {
		g.SSHUser = user
	}
	if password != "" {
		g.SSHPassword = password
	}
	if key != "" {
		g.SSHKey = key
	}
	if transport != "" {
		g.Transport = transport
	}
	switch {
	case shell != "":
		g.Shell = shell
	case g.Shell == "" && g.hasCreds():
		// The golden VM is not running and carries no port forward, so a
		// Windows guest cannot be asked here: probeShell answers "" for it and
		// the first exec against a fork settles it. --shell skips that.
		if g.OSType != "" {
			g.Shell = probeShell(g.OSType, nil)
		}
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
// from RAM in seconds instead of rebooting. Anything that fails once the clone
// is registered is rolled back, so a failed fork leaves no half-built env
// behind and returns a nil Env.
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
	// The one mutating step with nothing to roll back: no record exists yet,
	// and a failed clonevm removes the half-registered clone itself.
	if err := e.VB.CloneLinked(g.VMName, g.Snapshot, vmName); err != nil {
		return nil, err
	}

	// Recorded before boot so the rollback below has something to remove, and
	// so `terrarium rm` can still reach the VM if the process dies first.
	env := &Env{
		VMName:  vmName,
		Golden:  golden,
		SSHPort: port,
		Created: time.Now(),
		Share:   opts.ShareHostPath,
		// A clone runs the same guest as its golden, so the type is copied
		// rather than probed again. Blank if the golden has none yet; the
		// listing commands fill it in later.
		OSType: g.OSType,
	}
	if opts.TTL > 0 {
		env.Expires = env.Created.Add(opts.TTL)
	}
	if vm, err := e.findVM(vmName); err == nil {
		env.UUID = vm.UUID
	}
	e.St.Envs[name] = env
	if err := e.St.Save(); err != nil {
		return nil, e.rollbackFork(name, vmName, err, progress)
	}
	if err := e.prepareFork(g, env, opts, progress); err != nil {
		return nil, e.rollbackFork(name, vmName, err, progress)
	}
	return env, nil
}

// needsDiscard reports whether a machine has a saved RAM image that has to go
// before anything can reconfigure or unregister it. A clone of an online
// snapshot - one taken while the machine ran, which is what `adopt` often
// finds on a user's own VM - comes up Saved, and VirtualBox refuses modifyvm
// and unregistervm in that state. VirtualBox 7 reports aborted-saved for a VM
// process that died holding one, which is refused the same way; savingstate is
// a save in progress and is not ours to throw away.
func needsDiscard(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "saved", "aborted-saved":
		return true
	}
	return false
}

// stopForUnregister takes a VM to the only state unregistervm accepts: off,
// and holding no saved RAM image. Every path that unregisters needs it, not
// just the fork rollback - a Revert whose restore lands on an online snapshot
// and whose start then fails parks the env Saved, and WaitOff counts saved as
// down. A VM someone deleted outside terrarium reports gone: nothing left to
// stop, and nothing to unregister either. Problems are collected rather than
// returned one at a time, so a caller that pushes through cleanup can report
// them all and one that stops takes the first.
func (e *Engine) stopForUnregister(vmName string) (gone bool, problems []error) {
	if err := e.VB.PowerOff(vmName); err != nil {
		if vbox.IsNotRegistered(err) {
			return true, nil
		}
		problems = append(problems, err)
	}
	if err := e.VB.WaitOff(vmName, 30*time.Second); err != nil {
		if vbox.IsNotRegistered(err) {
			return true, problems
		}
		problems = append(problems, err)
	}
	if state, err := e.VB.VMState(vmName); err == nil && needsDiscard(state) {
		if err := e.VB.DiscardState(vmName); err != nil {
			problems = append(problems, err)
		}
	}
	return false, problems
}

// prepareFork configures and boots a clone that is already registered and
// recorded. Every error it returns is rolled back by Fork, so it cleans up
// nothing itself.
func (e *Engine) prepareFork(g *Golden, env *Env, opts ForkOpts, progress func(string)) error {
	vmName := env.VMName
	state, err := e.VB.VMState(vmName)
	if err != nil {
		return err
	}
	if needsDiscard(state) {
		// Costs a cold boot instead of resuming the golden's RAM: the only way
		// to get a mutable machine out of an online snapshot.
		progress("clone came up saved (online snapshot): discarding saved state, it will cold boot")
		if err := e.VB.DiscardState(vmName); err != nil {
			return err
		}
	}
	if err := e.VB.SetNATSSH(vmName, env.SSHPort); err != nil {
		return err
	}
	if err := e.VB.ModifyCPUMem(vmName, opts.CPUs, opts.MemMB); err != nil {
		return err
	}
	// Forks inherit the golden's pointing hardware, PS/2-only on most images.
	// Absolute mouse injection (click) needs a USB tablet, and it has to land
	// before the clean snapshot so revert keeps the mouse.
	if err := e.VB.EnableMouseTablet(vmName); err != nil {
		return err
	}
	// Attached before the clean snapshot, so revert keeps the share.
	if opts.ShareHostPath != "" {
		if err := e.VB.SharedFolderAdd(vmName, ShareName, opts.ShareHostPath); err != nil {
			return err
		}
	}
	progress("booting")
	if err := e.VB.StartHeadless(vmName); err != nil {
		return err
	}
	if TransportOf(g) == TransportGuestControl {
		if err := e.waitGuestControl(env, g, progress); err != nil {
			return err
		}
	} else if g.hasCreds() {
		if err := sshx.WaitReady(env.SSHPort, bootTimeout); err != nil {
			return err
		}
	} else {
		progress(credlessNote)
		// Nothing to wait for, but snapshotting the instant the VM starts
		// would leave the revert target at the BIOS. Give it some boot.
		time.Sleep(settleTime)
	}
	progress("snapshotting clean state (revert target)")
	return e.VB.TakeSnapshot(vmName, cleanSnapshot)
}

// rollbackFork undoes a fork that failed after its clone was registered: the
// VM goes with its disks, and so does the env record. Cleanup pushes through
// its own errors instead of returning early, because a half-cleaned fork is
// worse than a reported one, and a VM someone deleted outside terrarium counts
// as already cleaned. It narrates: waiting for a VM to stop and retrying a
// locked unregister take minutes, and silence there reads as a hang.
func (e *Engine) rollbackFork(name, vmName string, cause error, progress func(string)) error {
	var problems []string
	fail := func(err error) { problems = append(problems, err.Error()) }

	progress("rolling back " + vmName)
	gone, stopped := e.stopForUnregister(vmName)
	for _, err := range stopped {
		fail(err)
	}
	if !gone {
		progress("unregistering " + vmName)
		if err := e.VB.Unregister(vmName); err != nil && !vbox.IsNotRegistered(err) {
			fail(err)
		}
	}
	delete(e.St.Envs, name)
	if err := e.St.Save(); err != nil {
		fail(err)
	}
	return rollbackErr(cause, vmName, problems)
}

// rollbackErr reports a rolled-back failure. The cause stays wrapped and in
// front: cleanup detail is appended to it, never substituted for it, or the
// reason the fork failed disappears behind the tidying up.
func rollbackErr(cause error, vmName string, problems []string) error {
	if len(problems) == 0 {
		return fmt.Errorf("%w (rolled back, nothing left behind)", cause)
	}
	return fmt.Errorf("%w\nrollback of %s did not finish (%s): remove the leftovers with `%s`",
		cause, vmName, strings.Join(problems, "; "), vbox.ManualDeleteHint(vmName))
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
	g := e.St.Goldens[env.Golden]
	if TransportOf(g) == TransportGuestControl {
		return e.waitGuestControl(env, g, progress)
	}
	if !g.hasCreds() {
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
	if env.Golden == "" {
		// The clean snapshot of an ISO-installed env is the blank disk it was
		// created with, so this is not "back to a working machine" - it is
		// starting the installation over.
		progress("restoring clean state (blank disk: this restarts the install)")
	} else {
		progress("restoring clean state")
	}
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
	gone, problems := e.stopForUnregister(env.VMName)
	if len(problems) > 0 {
		return problems[0]
	}
	if !gone {
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
	// An env from `terrarium create` has no golden at all: nobody has told
	// terrarium what account the OS being installed inside it ends up with,
	// and there may not be one yet. Point at the way out rather than at
	// adopting a golden that does not exist.
	if env.Golden == "" {
		return 0, "", "", "", fmt.Errorf("env %q has no credentials: it was created from an ISO, so drive it with `terrarium screenshot/type/keys/click`.\nonce the install is finished: `terrarium promote %s <image>`, then `terrarium adopt %s<image> --user <user> [--password <pw> | --key <path>]`",
			name, name, goldenPrefix)
	}
	g := e.St.Goldens[env.Golden]
	if g == nil || g.SSHUser == "" {
		vmName := ""
		if g != nil {
			vmName = g.VMName
		}
		return 0, "", "", "", fmt.Errorf("golden %q has no SSH user: work the login out through screenshot/type, then record it with `%s`",
			env.Golden, AdoptHint(vmName, env.Golden))
	}
	return env.SSHPort, g.SSHUser, g.SSHPassword, g.SSHKey, nil
}
