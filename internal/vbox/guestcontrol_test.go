package vbox

import (
	"strings"
	"testing"
	"time"
)

// VBoxManage sets argv[0] to the image itself, so anything passed after -- is
// an argument to the program. Repeating the program name there is the classic
// way to get a shell that ignores its first switch.
func TestGuestRunArgs(t *testing.T) {
	args := guestRunArgs("trr-w", GuestCreds{User: "u", Password: "p"}, 90*time.Second,
		`C:\Windows\System32\cmd.exe`, []string{"/c", "ver"})
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"guestcontrol trr-w run",
		"--username u",
		"--password p",
		"--wait-stdout --wait-stderr",
		"--timeout 90000",
		`--exe C:\Windows\System32\cmd.exe`,
		"-- /c ver",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in: %s", want, joined)
		}
	}
	if i := indexOf(args, "--"); i < 0 || args[i+1] != "/c" {
		t.Errorf("the first argument after -- must be the program's own first argument, got %v", args)
	}
}

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return -1
}

// VBoxManage reports a guest exit code as its own, offset by 32 and clamped at
// 126. Measured against VirtualBox 7.2: guest 1 came back 33, guest 7 came
// back 39, and everything from 95 up came back 126. Reading the offset wrong
// turns a successful command into a failure or the other way round.
func TestGuestExitCode(t *testing.T) {
	for _, c := range []struct {
		name     string
		vboxExit int
		want     int
		wantOK   bool
	}{
		{"success", 0, 0, true},
		{"guest exit 1", 33, 1, true},
		{"guest exit 7", 39, 7, true},
		{"guest exit 93", 125, 93, true},
		{"anything from 94 up saturates", 126, 126, true},
		{"VBoxManage's own failure", 1, -1, false},
		{"a syntax error in our own arguments", 2, -1, false},
		{"the offset itself is never a guest code", 32, -1, false},
	} {
		got, ok := guestExitCode(c.vboxExit)
		if ok != c.wantOK || (ok && got != c.want) {
			t.Errorf("%s: got (%d, %v), want (%d, %v)", c.name, got, ok, c.want, c.wantOK)
		}
	}
}

// terrarium's guest paths are forward-slashed everywhere, Windows included,
// but the copy verbs hand the path to the guest's own file API.
func TestGuestWindowsPath(t *testing.T) {
	for in, want := range map[string]string{
		"C:/Users/terrarium/x.txt": `C:\Users\terrarium\x.txt`,
		"/tmp/x.txt":               "/tmp/x.txt",
		"x.txt":                    "x.txt",
	} {
		if got := guestWindowsPath(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

// The guest's password is on the VBoxManage command line, so an error may
// quote what VirtualBox said and never what was passed to it.
func TestRedactedTailKeepsOnlyVirtualBoxsWords(t *testing.T) {
	out := "VBoxManage.exe: error: File copy failed\nVBoxManage.exe: error: Destination exists\n"
	got := redactedTail(out)
	if !strings.Contains(got, "File copy failed") {
		t.Errorf("the useful line was dropped: %s", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("the tail should be one line: %q", got)
	}
}
