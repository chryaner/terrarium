package core

import (
	"context"
	"fmt"
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
	case transportOf(g) == TransportGuestControl && (g.SSHUser == "" || g.SSHPassword == ""):
		return fmt.Errorf("a hostname cannot be set on this golden: it records no login to run the rename with")
	case transportOf(g) == TransportSSH && !g.hasCreds():
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

// consoleStall is how long a booting guest's console may stay silent, with
// sshd still not answering, before the boot is taken to have hung.
const consoleStall = 75 * time.Second

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

// waitSSH waits for a booting guest's sshd. Linux guests under VirtualBox now
// and then stop dead in the first seconds of the kernel's boot and never
// recover, while the same machine boots normally when restarted. So a guest
// whose console has gone silent without sshd coming up gets the reset button
// once, and the full timeout again. A guest with no console log gives nothing
// to judge by and is simply waited for.
func (e *Engine) waitSSH(vmName string, port int, timeout time.Duration, progress func(string)) error {
	console := ""
	if info, err := e.VB.Info(vmName); err == nil {
		console = serialLogOf(info)
	}
	stalled := stallWatch{path: console, limit: consoleStall}
	deadline := time.Now().Add(timeout)
	reset := false
	for {
		if sshx.WaitReady(port, 3*time.Second) == nil {
			return nil
		}
		now := time.Now()
		if !reset && stalled.check(now) {
			progress(fmt.Sprintf("the guest console has been silent for %s with no sshd: resetting the machine once", consoleStall))
			if err := e.VB.Reset(vmName); err != nil {
				return err
			}
			reset = true
			deadline = now.Add(timeout)
			continue
		}
		if now.After(deadline) {
			return fmt.Errorf("no SSH banner on 127.0.0.1:%d after %s", port, timeout)
		}
	}
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
