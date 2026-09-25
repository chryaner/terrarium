package core

import (
	"strings"
	"testing"
)

func TestParseCopySpec(t *testing.T) {
	cases := []struct {
		in       string
		windows  bool
		env, pth string
	}{
		{"t1:/tmp/x", true, "t1", "/tmp/x"},
		{"dev-box:/home/terrarium", true, "dev-box", "/home/terrarium"},
		// Everything after the first colon is the path, so a Windows guest
		// path survives intact.
		{"win:C:/Users/terrarium/x", true, "win", "C:/Users/terrarium/x"},
		{"./notes.txt", true, "", "./notes.txt"},
		{`C:\src\notes.txt`, true, "", `C:\src\notes.txt`},
		{"c:/src/notes.txt", true, "", "c:/src/notes.txt"},
		{"notes.txt", true, "", "notes.txt"},
		{`\\server\share\x`, true, "", `\\server\share\x`},
		// Not an env name, so not an env spec.
		{"../rel:x", true, "", "../rel:x"},
		// A Unix host has no drive letters, so a one-letter env is an env.
		{"a:/tmp/x", false, "a", "/tmp/x"},
		{"t1:/tmp/x", false, "t1", "/tmp/x"},
		{"/home/me/notes.txt", false, "", "/home/me/notes.txt"},
		{"../rel:x", false, "", "../rel:x"},
	}
	for _, c := range cases {
		got := parseCopySpec(c.in, c.windows)
		if got.Env != c.env || got.Path != c.pth {
			t.Errorf("parseCopySpec(%q, windows=%v) = %+v, want env %q path %q", c.in, c.windows, got, c.env, c.pth)
		}
	}
}

// pinWindowsHost makes planCopy parse like the given host for one test.
func pinWindowsHost(t *testing.T, windows bool) {
	t.Helper()
	old := windowsHost
	windowsHost = windows
	t.Cleanup(func() { windowsHost = old })
}

func TestPlanCopyDirection(t *testing.T) {
	pinWindowsHost(t, true)
	push, err := planCopy(`C:\src\a.txt`, "t1:/tmp/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !push.Push || push.Env != "t1" || push.Local != `C:\src\a.txt` || push.Remote != "/tmp/a.txt" {
		t.Errorf("push plan = %+v", push)
	}

	pull, err := planCopy("t1:/tmp/a.txt", `C:\dst`)
	if err != nil {
		t.Fatal(err)
	}
	if pull.Push || pull.Env != "t1" || pull.Local != `C:\dst` || pull.Remote != "/tmp/a.txt" {
		t.Errorf("pull plan = %+v", pull)
	}
}

func TestPlanCopyRejects(t *testing.T) {
	cases := []struct {
		name     string
		src, dst string
		wantErr  string
	}{
		{"local to local", "a.txt", "b.txt", "neither side names an env"},
		{"env to env", "t1:/a", "t2:/b", "both sides name an env"},
		{"no guest path", "a.txt", "t1:", "no path after t1:"},
		{"no host path", "t1:/a", "", "no path on the host side"},
	}
	for _, c := range cases {
		_, err := planCopy(c.src, c.dst)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.wantErr)
		}
	}
}

func TestPlanCopyOneLetterEnvOnUnixHost(t *testing.T) {
	pinWindowsHost(t, false)
	plan, err := planCopy("/home/me/a.txt", "a:/tmp/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Push || plan.Env != "a" || plan.Local != "/home/me/a.txt" || plan.Remote != "/tmp/a.txt" {
		t.Errorf("plan = %+v", plan)
	}
}
