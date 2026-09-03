package core

import (
	"fmt"
	"io"
	"strings"

	"github.com/chryaner/terrarium/internal/sshx"
)

// The shells an SSH session can land in, and the only values Golden.Shell
// takes. Which one it is decides how a command's argv has to be quoted, and
// there is no quoting that works for more than one of them.
const (
	ShellPOSIX      = "posix"
	ShellCmd        = "cmd"
	ShellPowerShell = "powershell"
)

// Shells lists the values in the order help text should show them.
var Shells = []string{ShellPOSIX, ShellCmd, ShellPowerShell}

// ValidShell reports whether s names a shell terrarium knows how to quote for.
func ValidShell(s string) bool {
	return s == ShellPOSIX || s == ShellCmd || s == ShellPowerShell
}

// ScriptCommand is the shell reading a whole script from its own stdin.
// Neither line has a metacharacter in it, which is the point: whatever shell
// sshd hands it to passes it on unchanged, and the script itself is parsed
// only by its intended reader.
//
// cmd.exe has no stdin script mode worth having, so a Windows guest gets
// PowerShell for scripts whichever shell its sessions land in - a cmd guest
// still has powershell.exe.
func ScriptCommand(shell string) string {
	if shell == ShellPOSIX {
		return "sh -s"
	}
	return "powershell -NoProfile -NonInteractive -Command -"
}

// The two ways to hand PowerShell a command on a command line. -Command takes
// it as text, which only works where nothing re-parses that text on the way;
// -EncodedCommand takes base64, which survives anything.
const (
	psCommandPrefix = "powershell -NoProfile -NonInteractive -Command "
	psEncodedPrefix = "powershell -NoProfile -NonInteractive -EncodedCommand "
)

// LaunchCmd builds the line that runs cmdLine under cmd.exe. have is the shell
// sshd hands the whole line to, and it parses the line before cmd.exe sees any
// of it: on a PowerShell guest an unquoted `cmd /c echo $env:USERNAME` is
// expanded by PowerShell first, and a $(...) or ; in the command runs there
// rather than in cmd. So the line crosses as one quoted argument.
func LaunchCmd(have, cmdLine string) string {
	switch have {
	case ShellPowerShell:
		return "cmd /c " + sshx.QuotePowerShellArg(cmdLine)
	case ShellPOSIX:
		return "cmd /c " + sshx.QuotePOSIXArg(cmdLine)
	default:
		// A cmd guest is the one case with nothing to quote against: cmd.exe
		// has no quoting that rebuilds a command line, and it is also the
		// parser the line is written for, so it goes over as typed.
		return "cmd /c " + cmdLine
	}
}

// LaunchPowerShell builds the line that runs psCommand under PowerShell.
// Anywhere but a PowerShell guest that means -EncodedCommand: cmd.exe splits
// on & and | inside single quotes, double quotes and carets alike, so a
// quoted PowerShell command line handed to a cmd guest runs half in cmd.
func LaunchPowerShell(have, psCommand string) string {
	if have == ShellPowerShell {
		return psCommandPrefix + psCommand
	}
	return psEncodedPrefix + sshx.EncodePowerShell(psCommand)
}

// LaunchSh builds the line that runs shLine under a POSIX shell. On a
// PowerShell guest the whole script is one PowerShell argument; anywhere else
// POSIX quoting is what reads it, which is also what a cmd guest passes
// through untouched.
func LaunchSh(have, shLine string) string {
	if have == ShellPowerShell {
		return "sh -c " + sshx.QuotePowerShellArg(shLine)
	}
	return sshx.QuotePOSIX([]string{"sh", "-c", shLine})
}

// shellProbe asks a Windows guest which shell it dropped the session into.
// cmd.exe expands %COMSPEC% to its own path; PowerShell has no % expansion
// and echoes the word back untouched.
const shellProbe = "echo %COMSPEC%"

// classifyShellProbe reads shellProbe's output. An answer it does not
// recognise returns "" so the caller can keep the conservative default rather
// than quote for a shell that may not be there.
func classifyShellProbe(out string) string {
	s := strings.TrimSpace(out)
	switch {
	case strings.Contains(strings.ToLower(s), "cmd.exe"):
		return ShellCmd
	case strings.Contains(s, "%COMSPEC%"):
		return ShellPowerShell
	}
	return ""
}

// probeShell decides a guest's shell from its VirtualBox guest type, asking
// the guest itself only when it has to. Everything that is not Windows runs a
// POSIX shell; a Windows guest could be either, and only it knows which - a
// golden terrarium built recently answers PowerShell, an older or adopted one
// cmd.
//
// ask runs shellProbe in the guest. A nil ask, or one that fails, means the
// answer is not available right now: the caller gets "" and probes later,
// rather than recording a guess that exec would then quote against.
func probeShell(ostype string, ask func() (string, error)) string {
	if !isWindowsGuest(ostype) {
		return ShellPOSIX
	}
	if ask == nil {
		return ""
	}
	out, err := ask()
	if err != nil {
		return ""
	}
	return classifyShellProbe(out)
}

// ShellFor reports how commands for an env's guest have to be quoted, and
// remembers the answer on the golden. Goldens recorded before this existed
// carry no shell, so the first exec against one pays a probe and every exec
// after it reads the record.
func (e *Engine) ShellFor(envName string) (string, error) {
	env := e.St.Envs[envName]
	if env == nil {
		return "", fmt.Errorf("no env %q", envName)
	}
	g := e.St.Goldens[env.Golden]
	if g != nil && g.Shell != "" {
		return g.Shell, nil
	}
	ostype, err := e.osType(env.VMName, &env.OSType)
	if err != nil {
		return "", err
	}
	// Nothing to probe on the guestcontrol transport: Guest Additions start a
	// program rather than a session, so there is no shell for the guest to be
	// asked about - the record is what decides which one gets launched. Asking
	// anyway would also recurse, since running the probe goes through here.
	var ask func() (string, error)
	if transportOf(g) == TransportSSH {
		ask = func() (string, error) {
			port, user, password, key, err := e.SSHTarget(envName)
			if err != nil {
				return "", err
			}
			var out sshx.OutputBuffer
			if _, err := sshx.ExecStreams(port, user, password, key, shellProbe, &out, io.Discard); err != nil {
				return "", err
			}
			return out.String(), nil
		}
	}
	shell := probeShell(ostype, ask)
	if shell == "" {
		// Every Windows golden built before the post-install set DefaultShell
		// runs cmd, and cmd is the quoting exec has always used there, so an
		// unreachable or unreadable guest costs nothing by assuming it.
		return ShellCmd, nil
	}
	if g != nil {
		g.Shell = shell
		if err := e.St.Save(); err != nil {
			return shell, err
		}
	}
	return shell, nil
}
