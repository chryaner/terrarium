package core

import (
	"strings"
	"testing"
)

func TestParseCopySpec(t *testing.T) {
	cases := []struct {
		in       string
		env, pth string
	}{
		{"t1:/tmp/x", "t1", "/tmp/x"},
		{"dev-box:/home/terrarium", "dev-box", "/home/terrarium"},
		// Everything after the first colon is the path, so a Windows guest
		// path survives intact.
		{"win:C:/Users/terrarium/x", "win", "C:/Users/terrarium/x"},
		{"./notes.txt", "", "./notes.txt"},
		{`C:\src\notes.txt`, "", `C:\src\notes.txt`},
		{"c:/src/notes.txt", "", "c:/src/notes.txt"},
		{"notes.txt", "", "notes.txt"},
		{`\\server\share\x`, "", `\\server\share\x`},
		// Not an env name, so not an env spec.
		{"../rel:x", "", "../rel:x"},
	}
	for _, c := range cases {
		got := parseCopySpec(c.in)
		if got.Env != c.env || got.Path != c.pth {
			t.Errorf("parseCopySpec(%q) = %+v, want env %q path %q", c.in, got, c.env, c.pth)
		}
	}
}

func TestPlanCopyDirection(t *testing.T) {
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
