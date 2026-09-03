package core

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chryaner/terrarium/internal/sshx"
	"github.com/chryaner/terrarium/internal/vbox"
)

// Every golden written before the field existed has to keep working, and the
// only way that holds is if a blank transport means SSH.
func TestTransportOf(t *testing.T) {
	for _, c := range []struct {
		name string
		g    *Golden
		want string
	}{
		{"a record written before the field", &Golden{}, TransportSSH},
		{"an SSH record", &Golden{Transport: TransportSSH}, TransportSSH},
		{"a guest additions record", &Golden{Transport: TransportGuestControl}, TransportGuestControl},
		{"a value nobody recognises is not guessed at", &Golden{Transport: "winrm"}, TransportSSH},
		{"no golden at all", nil, TransportSSH},
	} {
		if got := transportOf(c.g); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	for _, bad := range []string{"winrm", "SSH", "guest-control", "telnet"} {
		if validTransport(bad) {
			t.Errorf("--transport %q should be rejected", bad)
		}
	}
	for _, ok := range Transports {
		if !validTransport(ok) {
			t.Errorf("--transport %q should be accepted", ok)
		}
	}
}

// Guest Additions start a program, not a session, so the shell has to be
// named. Which one is the shell the golden records - the same one the caller
// quoted the command for.
func TestGuestShellExe(t *testing.T) {
	for _, c := range []struct {
		shell    string
		wantExe  string
		wantArgs string
	}{
		{ShellPOSIX, "/bin/sh", "-c"},
		{ShellCmd, `C:\Windows\System32\cmd.exe`, "/c"},
		{ShellPowerShell, windowsDefaultShell, "-NoProfile -NonInteractive -Command"},
	} {
		exe, args := guestShellExe(c.shell)
		if exe != c.wantExe {
			t.Errorf("%s: exe got %q, want %q", c.shell, exe, c.wantExe)
		}
		if got := strings.Join(args, " "); got != c.wantArgs {
			t.Errorf("%s: args got %q, want %q", c.shell, got, c.wantArgs)
		}
	}
}

// There is no stdin to hand a shell over Guest Additions, so a script becomes
// a file in the guest. cmd.exe cannot read one at all, so a Windows guest gets
// PowerShell for it either way - the same rule ScriptCommand follows.
func TestGuestScriptTarget(t *testing.T) {
	for _, shell := range []string{ShellCmd, ShellPowerShell} {
		dir, ext, exe, argv := guestScriptTarget(shell)
		if ext != ".ps1" || exe != windowsDefaultShell {
			t.Errorf("%s: a Windows script should be a .ps1 run by PowerShell, got %s %s", shell, ext, exe)
		}
		if !strings.HasPrefix(dir, "C:/") {
			t.Errorf("%s: script directory %q is not a Windows path", shell, dir)
		}
		if got := strings.Join(argv(`C:\Windows\Temp\s.ps1`), " "); !strings.HasSuffix(got, `-File C:\Windows\Temp\s.ps1`) {
			t.Errorf("%s: args got %q", shell, got)
		}
	}
	dir, ext, exe, argv := guestScriptTarget(ShellPOSIX)
	if dir != "/tmp" || ext != ".sh" || exe != "/bin/sh" {
		t.Errorf("posix script target got %s %s %s", dir, ext, exe)
	}
	if got := strings.Join(argv("/tmp/s.sh"), " "); got != "/tmp/s.sh" {
		t.Errorf("posix script args got %q", got)
	}
}

// A path written into a script file has to be spelled the way the guest's own
// tools read it, whatever terrarium's forward-slash convention says.
func TestGuestNativePath(t *testing.T) {
	if got := guestNativePath(ShellCmd, "C:/Windows/Temp/x.ps1"); got != `C:\Windows\Temp\x.ps1` {
		t.Errorf("got %q", got)
	}
	if got := guestNativePath(ShellPOSIX, "/tmp/x.sh"); got != "/tmp/x.sh" {
		t.Errorf("got %q", got)
	}
}

// A deadline means the same thing on both transports - the wait was abandoned
// and the guest kept going - so it has to arrive as the same error, or
// kill-on-timeout only works over SSH.
func TestGuestTimeoutLooksLikeAnSSHTimeout(t *testing.T) {
	err := guestTimeoutAsSSH(&vbox.GuestTimeout{Timeout: 5 * time.Second}, 5*time.Second, "notepad")
	var timedOut *sshx.TimeoutError
	if !errors.As(err, &timedOut) {
		t.Fatalf("a guest timeout should be an sshx.TimeoutError, got %T", err)
	}
	if timedOut.Command != "notepad" || timedOut.Timeout != 5*time.Second {
		t.Errorf("the timeout lost its detail: %+v", timedOut)
	}
	// Everything else has to pass through untouched, or a real failure would
	// be reported as a timeout and the guest searched for a process that was
	// never started.
	other := errors.New("bad credentials")
	if got := guestTimeoutAsSSH(other, time.Second, "x"); got != other {
		t.Errorf("a non-timeout error was rewritten: %v", got)
	}
}
