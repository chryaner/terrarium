package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/chryaner/terrarium/internal/keys"
	"github.com/chryaner/terrarium/internal/sshx"
	"github.com/chryaner/terrarium/internal/vbox"
)

// rdpRule names the NAT forward `terrarium rdp` adds, so re-running replaces
// it rather than colliding with it.
const rdpRule = "rdp"

const guestRDPPort = 3389

// enableRDPCmd turns on the guest's own RDP server. VirtualBox's remote
// display would be less work, but VRDP lives in Oracle's proprietary extension
// pack - see docs/DESIGN.md - so Windows guests serve their own.
// $ProgressPreference first: the firewall cmdlets render a progress bar, and
// in a console-less SSH session that rendering fails with "Access is denied
// reading the console output buffer" and turns a successful enable into exit 1.
const enableRDPCmd = `powershell -Command "$ProgressPreference='SilentlyContinue'; Set-ItemProperty 'HKLM:\System\CurrentControlSet\Control\Terminal Server' -Name fDenyTSConnections -Value 0; Enable-NetFirewallRule -Name RemoteDesktop-UserMode-In-TCP"`

// runningEnv resolves an env that must be powered on. Everything in this file
// talks to a live console.
func (e *Engine) runningEnv(name string) (*Env, error) {
	env := e.St.Envs[name]
	if env == nil {
		return nil, fmt.Errorf("no env %q", name)
	}
	state, err := e.VB.VMState(env.VMName)
	if err != nil {
		return nil, err
	}
	if state != "running" {
		return nil, fmt.Errorf("env %q is not running: start it first", name)
	}
	return env, nil
}

// Screenshot captures the guest's screen to a PNG. This is the one way to see
// a guest that has no SSH, so it deliberately needs nothing of the guest.
func (e *Engine) Screenshot(envName, path string) error {
	env, err := e.runningEnv(envName)
	if err != nil {
		return err
	}
	return e.VB.ScreenshotPNG(env.VMName, path)
}

// TypeText types into the guest's keyboard as if a person were at it. The
// screen lags the keystrokes, so a screenshot taken immediately after may not
// show the result yet.
func (e *Engine) TypeText(envName, text string, pressEnter bool) error {
	env, err := e.runningEnv(envName)
	if err != nil {
		return err
	}
	if text != "" {
		if err := e.VB.KeyboardPutString(env.VMName, text); err != nil {
			return err
		}
	}
	if !pressEnter {
		return nil
	}
	seq, err := keys.Sequences([]string{"enter"})
	if err != nil {
		return err
	}
	return e.VB.KeyboardPutScancodes(env.VMName, seq)
}

// PressKeys sends named keys and chords: enter, alt-f4, ctrl-alt-del and the
// rest of keys.Names().
func (e *Engine) PressKeys(envName string, names []string) error {
	env, err := e.runningEnv(envName)
	if err != nil {
		return err
	}
	seq, err := keys.Sequences(names)
	if err != nil {
		return err
	}
	return e.VB.KeyboardPutScancodes(env.VMName, seq)
}

// clickGap paces press and release so the guest reads them as a click. Fast
// enough that a double-click's four events still land inside Windows'
// double-click window.
const clickGap = 60 * time.Millisecond

// Click presses and releases a mouse button at a screen position, in the same
// 0-based pixel coordinates Screenshot produces. A move lands first so the
// press happens where it was aimed, not where the pointer last was.
func (e *Engine) Click(envName string, x, y, button int, double bool) error {
	env, err := e.runningEnv(envName)
	if err != nil {
		return err
	}
	events := []vbox.MouseEvent{
		{X: x, Y: y},
		{X: x, Y: y, Buttons: button},
		{X: x, Y: y},
	}
	if double {
		events = append(events,
			vbox.MouseEvent{X: x, Y: y, Buttons: button},
			vbox.MouseEvent{X: x, Y: y},
		)
	}
	return e.VB.PutMouseEvents(env.VMName, events, clickGap)
}

// MoveMouse places the pointer without pressing anything, for hover menus and
// aiming a scroll.
func (e *Engine) MoveMouse(envName string, x, y int) error {
	env, err := e.runningEnv(envName)
	if err != nil {
		return err
	}
	return e.VB.PutMouseEvents(env.VMName, []vbox.MouseEvent{{X: x, Y: y}}, 0)
}

// Scroll turns the wheel under the current pointer position. Positive clicks
// scroll down.
func (e *Engine) Scroll(envName string, clicks int) error {
	env, err := e.runningEnv(envName)
	if err != nil {
		return err
	}
	return e.VB.PutMouseWheel(env.VMName, clicks)
}

// Snapshot takes a named snapshot. A running env gets its RAM captured too, so
// restoring resumes rather than reboots.
func (e *Engine) Snapshot(envName, snapName string) error {
	env := e.St.Envs[envName]
	if env == nil {
		return fmt.Errorf("no env %q", envName)
	}
	if snapName == "" {
		return fmt.Errorf("snapshot name is required")
	}
	// VirtualBox allows duplicate snapshot names, and a second "clean" would
	// make revert ambiguous about which one it restores.
	if snapName == cleanSnapshot {
		return fmt.Errorf("%q is reserved for the fork-time snapshot: `terrarium revert %s` restores that one", cleanSnapshot, envName)
	}
	return e.VB.TakeSnapshot(env.VMName, snapName)
}

// RestoreSnap rewinds an env to a named snapshot. `terrarium revert` already
// covers the automatic one taken at fork time, so this refuses to guess.
func (e *Engine) RestoreSnap(envName, snapName string, progress func(string)) error {
	env := e.St.Envs[envName]
	if env == nil {
		return fmt.Errorf("no env %q", envName)
	}
	if snapName == "" {
		return fmt.Errorf("snapshot name is required (`terrarium revert %s` restores the clean state)", envName)
	}
	has, err := e.VB.HasSnapshot(env.VMName, snapName)
	if err != nil {
		return err
	}
	if !has {
		return fmt.Errorf("env %q has no snapshot %q", envName, snapName)
	}

	progress("powering off")
	if err := e.VB.PowerOff(env.VMName); err != nil {
		return err
	}
	if err := e.VB.WaitOff(env.VMName, 30*time.Second); err != nil {
		return err
	}
	progress("restoring " + snapName)
	if err := e.VB.SnapshotRestore(env.VMName, snapName); err != nil {
		return err
	}
	if err := e.VB.StartHeadless(env.VMName); err != nil {
		return err
	}
	return e.waitReady(env, progress)
}

// Show opens a GUI window onto an env. VirtualBox can only attach a window
// when the VM starts, so an env already running headless has to be stopped
// first.
func (e *Engine) Show(envName string) error {
	env := e.St.Envs[envName]
	if env == nil {
		return fmt.Errorf("no env %q", envName)
	}
	state, err := e.VB.VMState(env.VMName)
	if err != nil {
		return err
	}
	if err := e.VB.StartSeparate(env.VMName); err != nil {
		if state == "running" {
			return fmt.Errorf("%s is already running headless: `terrarium down %s` then `terrarium show %s` to get a window",
				envName, envName, envName)
		}
		return err
	}
	return nil
}

// EnableRDP turns on the Windows guest's own RDP server and forwards a host
// port to it. Needs working SSH credentials: it is a Windows convenience, not
// a way in to a guest terrarium cannot already reach.
func (e *Engine) EnableRDP(envName string, progress func(string)) (int, error) {
	env, err := e.runningEnv(envName)
	if err != nil {
		return 0, err
	}
	port, user, password, key, err := e.SSHTarget(envName)
	if err != nil {
		return 0, err
	}

	progress("enabling RDP in the guest")
	var out sshx.OutputBuffer
	code, err := sshx.ExecStreams(port, user, password, key, enableRDPCmd, &out, &out)
	if err != nil {
		return 0, err
	}
	if code != 0 {
		return 0, fmt.Errorf("enabling RDP failed (exit %d):\n%s", code, strings.TrimSpace(out.String()))
	}

	hostPort := env.RDPPort
	if hostPort == 0 {
		if hostPort, err = e.freePort(); err != nil {
			return 0, err
		}
	}
	if err := e.VB.NATForwardRunning(env.VMName, rdpRule, hostPort, guestRDPPort); err != nil {
		return 0, err
	}
	env.RDPPort = hostPort
	return hostPort, e.St.Save()
}
