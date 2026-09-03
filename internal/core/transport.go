package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/chryaner/terrarium/internal/sshx"
	"github.com/chryaner/terrarium/internal/vbox"
)

// How terrarium reaches a guest. SSH is the default and everything the CLI
// documents assumes it; guestcontrol is for a guest that cannot run an SSH
// server at all - a Windows old enough to predate OpenSSH - where VirtualBox
// Guest Additions are the only way in.
const (
	TransportSSH          = "ssh"
	TransportGuestControl = "guestcontrol"
)

// Transports lists the values in the order help text should show them.
var Transports = []string{TransportSSH, TransportGuestControl}

// guestCleanupTimeout bounds tidying a scratch file out of the guest. Short,
// because it runs after the command the caller was waiting for.
const guestCleanupTimeout = 30 * time.Second

// validTransport reports whether s names a transport terrarium can use.
func validTransport(s string) bool {
	return s == TransportSSH || s == TransportGuestControl
}

// transportOf reads a golden's transport. Empty means SSH: every record
// written before the field existed is an SSH one.
func transportOf(g *Golden) string {
	if g != nil && g.Transport == TransportGuestControl {
		return TransportGuestControl
	}
	return TransportSSH
}

// envTransport reports how an env's guest is reached.
func (e *Engine) envTransport(envName string) (string, error) {
	env := e.St.Envs[envName]
	if env == nil {
		return "", fmt.Errorf("no env %q", envName)
	}
	return transportOf(e.St.Goldens[env.Golden]), nil
}

// guestTarget resolves what a guestcontrol call needs: which VM, and the
// account inside it. The password is the only auth Guest Additions have, so a
// golden with only a key cannot use this transport.
func (e *Engine) guestTarget(envName string) (string, vbox.GuestCreds, error) {
	env := e.St.Envs[envName]
	if env == nil {
		return "", vbox.GuestCreds{}, fmt.Errorf("no env %q", envName)
	}
	g := e.St.Goldens[env.Golden]
	if g == nil || g.SSHUser == "" || g.SSHPassword == "" {
		return "", vbox.GuestCreds{}, fmt.Errorf("golden %q is on the guestcontrol transport, which needs a guest user and password: record them with `%s --transport guestcontrol --user <user> --password <pw>`",
			env.Golden, AdoptHint(goldenVMName(g), env.Golden))
	}
	return env.VMName, vbox.GuestCreds{User: g.SSHUser, Password: g.SSHPassword}, nil
}

func goldenVMName(g *Golden) string {
	if g == nil {
		return ""
	}
	return g.VMName
}

// guestShellExe is the program guestcontrol launches to read a command line,
// and the switch that makes it read one. Guest Additions start a program, not
// a session, so the shell has to be named explicitly - the same shell the
// golden records, so the quoting the caller used still applies.
func guestShellExe(shell string) (exe string, args []string) {
	switch shell {
	case ShellPowerShell:
		return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
			[]string{"-NoProfile", "-NonInteractive", "-Command"}
	case ShellCmd:
		return `C:\Windows\System32\cmd.exe`, []string{"/c"}
	default:
		return "/bin/sh", []string{"-c"}
	}
}

// runGuestControl is Run over Guest Additions. command is the same command
// line SSH would have carried, so everything above this point - quoting,
// markers, wrappers - is unchanged; only the way it crosses differs.
func (e *Engine) runGuestControl(ctx context.Context, envName string, timeout time.Duration, command string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	vmName, creds, err := e.guestTarget(envName)
	if err != nil {
		return -1, err
	}
	shell, err := e.ShellFor(envName)
	if err != nil {
		return -1, err
	}
	if stdin != nil {
		code, err := e.runGuestScript(ctx, vmName, creds, shell, timeout, stdin, stdout, stderr)
		return code, guestTimeoutAsSSH(err, timeout, command)
	}
	exe, args := guestShellExe(shell)
	code, err := e.VB.GuestRun(ctx, vmName, creds, timeout, exe, append(args, command), nil, stdout, stderr)
	return code, guestTimeoutAsSSH(err, timeout, command)
}

// guestTimeoutAsSSH gives a guestcontrol deadline the same shape an SSH one
// has, so kill-on-timeout and every error message above this point work the
// same whichever transport carried the command. Both mean the same thing: the
// wait was abandoned, the guest kept going.
func guestTimeoutAsSSH(err error, timeout time.Duration, command string) error {
	var gt *vbox.GuestTimeout
	if errors.As(err, &gt) {
		return &sshx.TimeoutError{Timeout: timeout, Command: command}
	}
	return err
}

// guestScriptTarget is how a script reaches a guestcontrol guest: there is no
// stdin to feed a shell, so the script is written to a file in the guest and
// the shell is pointed at it. cmd.exe cannot read a script at all, so a
// Windows guest gets PowerShell for one whichever shell its commands normally
// use - the same rule ScriptCommand follows over SSH.
func guestScriptTarget(shell string) (dir, ext, exe string, argv func(path string) []string) {
	if shell == ShellPOSIX {
		return "/tmp", ".sh", "/bin/sh", func(p string) []string { return []string{p} }
	}
	return `C:/Windows/Temp`, ".ps1", windowsDefaultShell,
		func(p string) []string {
			return []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", p}
		}
}

// runGuestScript writes the script into the guest and runs it from there. The
// file is removed afterwards even when the script failed: a scratch file left
// in the guest is litter, and a failed run is exactly when someone reverts.
func (e *Engine) runGuestScript(ctx context.Context, vmName string, creds vbox.GuestCreds, shell string, timeout time.Duration, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	script, err := io.ReadAll(stdin)
	if err != nil {
		return -1, err
	}
	dir, ext, exe, argv := guestScriptTarget(shell)
	local, err := os.CreateTemp("", "terrarium-script-*"+ext)
	if err != nil {
		return -1, err
	}
	defer os.Remove(local.Name())
	if _, err := local.Write(script); err != nil {
		local.Close()
		return -1, err
	}
	local.Close()

	guestPath := path.Join(dir, "trr-"+newMarkerID()+ext)
	if err := e.VB.GuestCopyTo(vmName, creds, local.Name(), guestPath, false); err != nil {
		return -1, err
	}
	defer e.removeGuestFile(vmName, creds, shell, guestPath)

	return e.VB.GuestRun(ctx, vmName, creds, timeout, exe, argv(guestNativePath(shell, guestPath)), nil, stdout, stderr)
}

// guestNativePath spells a path the way the guest's own tools want it.
func guestNativePath(shell, p string) string {
	if shell == ShellPOSIX {
		return p
	}
	return strings.ReplaceAll(p, "/", `\`)
}

// removeGuestFile deletes one scratch file from the guest. Best effort and
// unreported: the script it belonged to has already run, and a leftover file
// in C:\Windows\Temp is not worth failing that run over. cmd.exe does the
// deleting on Windows whatever shell the commands use, because `del` is a
// builtin of that shell and of no other.
func (e *Engine) removeGuestFile(vmName string, creds vbox.GuestCreds, shell, guestPath string) {
	exe, args := guestShellExe(shell)
	rm := "rm -f " + guestPath
	if shell != ShellPOSIX {
		rm = "del /q " + guestNativePath(ShellCmd, guestPath)
		exe, args = guestShellExe(ShellCmd)
	}
	e.VB.GuestRun(context.Background(), vmName, creds, guestCleanupTimeout, exe, append(args, rm), nil, io.Discard, io.Discard)
}

// copyGuestControl is Push/Pull over Guest Additions. parents is honoured on
// both sides: copyto refuses a destination whose directory does not exist, and
// the host end of a pull is ours to create.
func (e *Engine) copyGuestControl(envName, local, remote string, push, recursive, parents bool) error {
	vmName, creds, err := e.guestTarget(envName)
	if err != nil {
		return err
	}
	if push {
		if parents {
			if dir := path.Dir(remote); dir != "." && dir != "/" {
				if err := e.VB.GuestMkdirAll(vmName, creds, dir); err != nil {
					return err
				}
			}
		}
		return e.VB.GuestCopyTo(vmName, creds, local, remote, recursive)
	}
	if parents {
		if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
			return err
		}
	}
	return e.VB.GuestCopyFrom(vmName, creds, remote, local, recursive)
}

// waitGuestControl is the readiness wait for a guest with no sshd: the
// additions reporting a version and taking a command, in place of an SSH
// banner.
func (e *Engine) waitGuestControl(env *Env, g *Golden, progress func(string)) error {
	if g.SSHUser == "" || g.SSHPassword == "" {
		progress("guestcontrol transport with no credentials recorded: nothing to wait for")
		return nil
	}
	creds := vbox.GuestCreds{User: g.SSHUser, Password: g.SSHPassword}
	shell := g.Shell
	if shell == "" {
		shell = ShellCmd
	}
	exe, args := guestShellExe(shell)
	noop := "exit 0"
	if shell == ShellPOSIX {
		noop = "true"
	}
	progress("waiting for guest additions")
	return e.VB.GuestReady(context.Background(), env.VMName, creds, exe, append(args, noop), bootTimeout, progress)
}
