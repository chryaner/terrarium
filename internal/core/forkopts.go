package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chryaner/terrarium/internal/sshx"
	"github.com/chryaner/terrarium/internal/vbox"
)

const (
	serialLogName = "serial.log"
	// failedDirName, under the data directory, keeps what a failed fork's
	// machine showed: the rollback deletes the machine itself.
	failedDirName = "failed"
	// evidenceLines is how much of the guest console a failed fork quotes.
	evidenceLines = 15
	evidenceWidth = 160
	// hostnameTimeout bounds the one command that renames the guest.
	hostnameTimeout = 2 * time.Minute
	// restartDownTimeout is how long a guest gets to drop off the network
	// after being told to restart.
	restartDownTimeout = 90 * time.Second
	// netBIOSMax is the longest computer name Windows accepts.
	netBIOSMax = 15
)

// hostnameRe is one DNS label: what every guest accepts as a host name.
var hostnameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

// checkForkHostname refuses a --hostname terrarium could not apply, before
// anything is cloned: the name has to be typed into the guest, so the golden
// must be one terrarium can run a command in.
func checkForkHostname(host string, g *Golden) error {
	if host == "" {
		return nil
	}
	if !hostnameRe.MatchString(host) {
		return fmt.Errorf("invalid hostname %q: letters, digits and dashes, at most 63 characters", host)
	}
	windows := isWindowsGuest(g.OSType)
	switch {
	case isNT5(g.OSType):
		return fmt.Errorf("a hostname cannot be set on this golden: its Windows has no PowerShell to rename the computer with")
	case windows && len(host) > netBIOSMax:
		return fmt.Errorf("hostname %q is too long for Windows: at most %d characters", host, netBIOSMax)
	case windows && allDigits(host):
		return fmt.Errorf("hostname %q is all digits, which Windows refuses", host)
	case windows && transportOf(g) == TransportGuestControl:
		// Guest Additions run a command with the account's ordinary rights,
		// administrator or not, and renaming a computer needs elevation.
		return fmt.Errorf("a hostname cannot be set on this golden: renaming Windows needs elevated rights, which the guestcontrol transport does not have")
	case !canRun(g):
		return fmt.Errorf("a hostname cannot be set on this golden: it records no login to run the rename with")
	}
	return nil
}

func allDigits(s string) bool {
	return strings.Trim(s, "0123456789") == ""
}

// posixHostnameScript renames a Linux guest for good: the running kernel, the
// file the next boot reads, and the hosts entry that makes sudo quick to
// resolve its own name. host has passed hostnameRe, so it needs no quoting.
func posixHostnameScript(host string) string {
	return `if [ "$(id -u)" = 0 ]; then s=; else s="sudo -n"; fi; ` +
		`if command -v hostnamectl >/dev/null 2>&1; then $s hostnamectl set-hostname ` + host + `; ` +
		`else $s sh -c 'echo ` + host + ` > /etc/hostname; hostname ` + host + `'; fi && ` +
		`if grep -q '^127\.0\.1\.1' /etc/hosts; then $s sed -i 's/^127\.0\.1\.1.*/127.0.1.1 ` + host + `/' /etc/hosts; ` +
		`else $s sh -c 'echo 127.0.1.1 ` + host + ` >> /etc/hosts'; fi`
}

// windowsHostnameCommand renames a Windows guest. The name only takes effect
// after a restart, which the caller does.
func windowsHostnameCommand(host string) string {
	return "Rename-Computer -NewName '" + host + "' -Force -ErrorAction Stop"
}

// setHostname names the guest of a fork that is up and reachable. It runs
// before the clean snapshot, so revert keeps the name.
func (e *Engine) setHostname(envName string, env *Env, g *Golden, host string, progress func(string)) error {
	shell, err := e.ShellFor(envName)
	if err != nil {
		return err
	}
	command := posixHostnameScript(host)
	windows := shell != ShellPOSIX
	if windows {
		command = LaunchPowerShell(shell, windowsHostnameCommand(host))
	}
	if err := e.runQuiet(envName, hostnameTimeout, command); err != nil {
		return fmt.Errorf("setting the hostname: %w", err)
	}
	if !windows {
		return nil
	}

	progress("restarting the guest so the name takes effect")
	// The session often dies with the guest before the command returns, so
	// its error says nothing either way; what follows checks the result.
	var discard sshx.OutputBuffer
	e.run(context.Background(), envName, hostnameTimeout, "shutdown /r /t 0", nil, &discard, &discard)
	if transportOf(g) == TransportSSH {
		if err := waitSSHDown(env.SSHPort, restartDownTimeout); err != nil {
			return err
		}
	} else {
		// Guest Additions have no banner to watch disappear; give the
		// restart time to begin before asking them for a command again.
		time.Sleep(settleTime)
	}
	return e.waitReady(env, progress)
}

// runQuiet runs one command in an env and turns a non-zero exit into an error
// carrying what the command printed.
func (e *Engine) runQuiet(envName string, timeout time.Duration, command string) error {
	var out sshx.OutputBuffer
	code, err := e.run(context.Background(), envName, timeout, command, nil, &out, &out)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("exit %d: %s", code, strings.TrimSpace(out.String()))
	}
	return nil
}

// waitSSHDown returns once sshd has stopped answering, so the wait for it to
// come back cannot be satisfied by the guest that has not restarted yet.
func waitSSHDown(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if sshx.WaitReady(port, 2*time.Second) != nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("the guest was told to restart and was still answering on 127.0.0.1:%d after %s", port, timeout)
}

// serialLogFor decides whether a fork's first serial port should log to a
// file of its own, and where. Empty means leave the port alone.
//
// A port already logging to a file is the golden's, path included, so it is
// always pointed at the fork's own folder. A port that is off is switched on
// for anything but Windows: Linux cloud images send their console to the
// first serial port, and without one that output goes nowhere. A port wired
// to a pipe, a socket or a host device was set up by someone on purpose.
func serialLogFor(info map[string]string, ostype string) string {
	dir := vbox.ConfigDir(info)
	if dir == "" {
		return ""
	}
	uart, mode := info["uart1"], info["uartmode1"]
	switch {
	case uart == "" || uart == "off":
		if isWindowsGuest(ostype) {
			return ""
		}
	case mode == "disconnected" || strings.HasPrefix(mode, "file,"):
	default:
		return ""
	}
	return filepath.Join(dir, serialLogName)
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// consoleTail is the last lines of a guest console log, made printable: boot
// output is full of colour codes and carriage returns.
func consoleTail(data []byte, lines, width int) []string {
	text := ansiRe.ReplaceAllString(string(data), "")
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.Map(func(r rune) rune {
			if r == '\t' {
				return ' '
			}
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, line)
		line = strings.TrimRight(line, " ")
		if line == "" {
			continue
		}
		if len(line) > width {
			line = line[:width]
		}
		out = append(out, line)
	}
	if len(out) > lines {
		out = out[len(out)-lines:]
	}
	return out
}

// bootEvidence collects what a fork's machine showed before a failed fork is
// rolled back: the tail of its serial console for the error message, and that
// log and a screenshot kept under the data directory. Best effort throughout:
// it must never hide the failure it is describing.
func (e *Engine) bootEvidence(name string, env *Env) string {
	info, err := e.VB.Info(env.VMName)
	if err != nil {
		return ""
	}
	keep := ""
	if e.St.path != "" {
		keep = filepath.Join(filepath.Dir(e.St.path), failedDirName)
		if err := os.MkdirAll(keep, 0o700); err != nil {
			keep = ""
		}
	}

	var b strings.Builder
	var kept []string
	if dir := vbox.ConfigDir(info); dir != "" {
		if data, err := os.ReadFile(filepath.Join(dir, serialLogName)); err == nil {
			if tail := consoleTail(data, evidenceLines, evidenceWidth); len(tail) > 0 {
				b.WriteString("the guest console ended with:\n")
				for _, line := range tail {
					b.WriteString("  " + line + "\n")
				}
			} else {
				b.WriteString("the guest printed nothing on its serial console\n")
			}
			if keep != "" {
				dst := filepath.Join(keep, name+".serial.log")
				if os.WriteFile(dst, data, 0o600) == nil {
					kept = append(kept, dst)
				}
			}
		}
	}
	if keep != "" && info["VMState"] == "running" {
		dst := filepath.Join(keep, name+".png")
		if e.VB.ScreenshotPNG(env.VMName, dst) == nil {
			kept = append(kept, dst)
		}
	}
	if len(kept) > 0 {
		b.WriteString("kept for inspection: " + strings.Join(kept, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// A booting guest whose console goes silent with sshd still not answering is
// taken to have hung. A fork or a restart of an env boots a machine that has
// booted before, in seconds, so half a minute of silence is already a hang.
// On a host where it happens at all it happens to about every second boot, so
// the resets have to be many for a fork to be dependable: six leave less than
// one failure in a hundred. A golden's first boot runs cloud-init, which has
// quiet stretches of its own and should not be interrupted lightly.
const (
	bootStall       = 30 * time.Second
	bootResets      = 6
	firstBootStall  = 90 * time.Second
	firstBootResets = 4
	// loginTail is how much of the end of a console log is searched for a
	// login prompt.
	loginTail = 2048
)

// serialLogOf is the file a VM's first serial port writes to, if it has one.
func serialLogOf(info map[string]string) string {
	if uart := info["uart1"]; uart == "" || uart == "off" {
		return ""
	}
	path, ok := strings.CutPrefix(info["uartmode1"], "file,")
	if !ok {
		return ""
	}
	return path
}

// waitBoot waits for the sshd of an env that has booted before.
func (e *Engine) waitBoot(vmName string, port int, progress func(string)) error {
	return e.waitSSH(vmName, port, bootTimeout, bootStall, bootResets, progress)
}

// waitSSH waits for a booting guest's sshd. Linux guests under VirtualBox now
// and then stop dead in the first seconds of the kernel's boot and never
// recover, while the same machine boots normally when restarted. So a guest
// whose console has been silent for stall without sshd coming up gets the
// reset button, up to resets times, and the full timeout again each time.
//
// Two cases are told apart from that hang. A console showing a login prompt
// belongs to a guest that finished booting and simply has no sshd to offer;
// resetting it would only repeat the same boot, so it is waited for. And a
// guest with no console log gives nothing to judge by.
func (e *Engine) waitSSH(vmName string, port int, timeout, stall time.Duration, resets int, progress func(string)) error {
	console := ""
	if info, err := e.VB.Info(vmName); err == nil {
		console = serialLogOf(info)
	}
	watch := stallWatch{path: console, limit: stall}
	defer e.nudging(vmName)()
	deadline := time.Now().Add(timeout)
	for done := 0; ; {
		if sshx.WaitReady(port, 3*time.Second) == nil {
			return nil
		}
		now := time.Now()
		if watch.check(now) && !atLoginPrompt(console) {
			if done == resets {
				return fmt.Errorf("the guest hung while booting %d times in a row: its console went silent with no sshd on 127.0.0.1:%d", resets+1, port)
			}
			done++
			progress(fmt.Sprintf("the guest console has been silent for %s with no sshd: resetting the machine (%d of %d)", stall, done, resets))
			if err := e.VB.Reset(vmName); err != nil {
				return err
			}
			watch = stallWatch{path: console, limit: stall}
			deadline = now.Add(timeout)
			continue
		}
		if now.After(deadline) {
			return fmt.Errorf("no SSH banner on 127.0.0.1:%d after %s", port, timeout)
		}
	}
}

// atLoginPrompt reports whether a console log ends at a login prompt: the
// sign of a guest that booted all the way.
func atLoginPrompt(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > loginTail {
		f.Seek(-loginTail, io.SeekEnd)
	}
	tail, err := io.ReadAll(f)
	return err == nil && strings.Contains(string(tail), "login:")
}

// stallWatch notices a log file that has stopped growing. Size, not the
// modification time: Windows does not update that while the writer holds the
// file open.
type stallWatch struct {
	path  string
	limit time.Duration

	size    int64
	changed time.Time
}

// check reports whether the file has content and has not grown for limit.
func (w *stallWatch) check(now time.Time) bool {
	if w.path == "" {
		return false
	}
	fi, err := os.Stat(w.path)
	if err != nil {
		return false
	}
	if w.changed.IsZero() || fi.Size() != w.size {
		w.size, w.changed = fi.Size(), now
		return false
	}
	return w.size > 0 && now.Sub(w.changed) >= w.limit
}

// canRun reports whether terrarium holds a login it can run a command with
// in a golden's guests.
func canRun(g *Golden) bool {
	if transportOf(g) == TransportGuestControl {
		return g.SSHUser != "" && g.SSHPassword != ""
	}
	return g.hasCreds()
}

func (e *Engine) newMACs(vmName string, nics []int) error {
	for _, nic := range nics {
		if err := e.VB.NewMAC(vmName, nic); err != nil {
			return err
		}
	}
	return nil
}

// unpinNetworkScript removes the card address from a Linux guest's network
// settings, in each place the common distributions keep one, so the same
// settings apply to whatever address the card has next. cloud-init wrote most
// of them and would write them again, so its network module is switched off.
const unpinNetworkScript = `if [ "$(id -u)" = 0 ]; then s=; else s="sudo -n"; fi
unpin() { if [ -d "$1" ]; then $s find "$1" -maxdepth 1 -type f -name "$2" -exec sed -i "$3" {} +; fi; }
unpin /etc/netplan '*.yaml' '/^ *macaddress:/d; /^ *set-name:/d; /^ *match: *$/d'
unpin /etc/NetworkManager/system-connections '*' '/^mac-address=/d'
unpin /etc/sysconfig/network-scripts 'ifcfg-*' '/^HWADDR=/d; /^MACADDR=/d'
unpin /etc/sysconfig/network 'ifcfg-*' '/^LLADDR=/d'
$s rm -f /etc/udev/rules.d/70-persistent-net.rules
if [ -d /etc/cloud/cloud.cfg.d ]; then
  echo 'network: {config: disabled}' | $s tee /etc/cloud/cloud.cfg.d/99-terrarium-network.cfg >/dev/null
fi
$s sync
`

// renewMACs gives a running Linux fork new card addresses: unpin the guest's
// network settings from the old one, shut down, change the hardware, boot
// again. One restart dearer than changing it before the first boot, and the
// only order in which the guest still has a network afterwards.
func (e *Engine) renewMACs(name string, env *Env, nics []int, progress func(string)) error {
	progress("freeing the guest's network settings from the golden's card address")
	var out sshx.OutputBuffer
	code, err := e.run(context.Background(), name, hostnameTimeout, "sh -s", strings.NewReader(unpinNetworkScript), &out, &out)
	if err != nil {
		return fmt.Errorf("preparing the guest for a new card address: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("preparing the guest for a new card address: exit %d: %s", code, strings.TrimSpace(out.String()))
	}
	progress("restarting with new network card addresses")
	if err := e.Down(name); err != nil {
		return err
	}
	if err := e.newMACs(env.VMName, nics); err != nil {
		return err
	}
	if err := e.VB.StartHeadless(env.VMName); err != nil {
		return err
	}
	return e.waitReady(env, progress)
}

// nudgeEvery is how often a guest that is being waited for gets nudged.
const nudgeEvery = 5 * time.Second

// nudging pokes a VM at intervals for as long as something waits on it, and
// returns the function that stops it.
//
// The hang waitSSH resets its way out of is, as far as it could be observed, a
// virtual CPU waiting for a wake-up that was lost, and any outside request to
// the VM process can deliver one: a Windows installer that had sat frozen for
// 66 minutes moved the moment its screen was captured, and about one hung
// Linux boot in three does the same. A screenshot changes nothing in the
// guest, so it is the nudge. It is the only remedy for a guest with no
// console log to judge a hang by, a Windows install above all, and for the
// rest it often saves the reset.
func (e *Engine) nudging(vmName string) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(nudgeEvery)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				e.nudge(vmName)
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

// nudge captures the VM's screen and throws the picture away. Errors do not
// matter: a VM with no display yet has nothing to capture and nothing to wake.
func (e *Engine) nudge(vmName string) {
	f, err := os.CreateTemp("", "terrarium-nudge-*.png")
	if err != nil {
		return
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	e.VB.ScreenshotPNG(vmName, path)
}
