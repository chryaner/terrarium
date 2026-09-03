package sshx

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"
)

// testClient runs a real sftp server over a pair of pipes, so the copy logic
// is exercised end to end against the same protocol a guest speaks - no VM,
// no network, no SSH. The server serves this machine's filesystem, so the
// "guest" side of each test is just another temp directory.
func testClient(t *testing.T) *sftp.Client {
	t.Helper()
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()

	srv, err := sftp.NewServer(struct {
		io.Reader
		io.WriteCloser
	}{serverRead, serverWrite})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve()

	c, err := sftp.NewClientPipe(clientRead, clientWrite)
	if err != nil {
		t.Fatal(err)
	}
	// Server first: the client's receive loop is parked on its pipe, and
	// closing the server's write end is the only thing that unblocks it.
	// Closing the client first deadlocks waiting for that goroutine.
	t.Cleanup(func() {
		srv.Close()
		c.Close()
	})
	return c
}

// guestPath spells a host path the way an SFTP client has to: forward slashes,
// which is also how a Windows guest's paths must be written.
func guestPath(p string) string { return filepath.ToSlash(p) }

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPushFile(t *testing.T) {
	c := testClient(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	write(t, src, "hello guest")

	if err := Push(c, src, guestPath(filepath.Join(dir, "dst.txt")), false, false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "dst.txt")); got != "hello guest" {
		t.Errorf("dst.txt = %q", got)
	}
}

// A destination that is an existing directory takes the source's own name,
// the way scp behaves.
func TestPushFileIntoDirectory(t *testing.T) {
	c := testClient(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	write(t, src, "into dir")
	into := filepath.Join(dir, "into")
	if err := os.Mkdir(into, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Push(c, src, guestPath(into), false, false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(into, "src.txt")); got != "into dir" {
		t.Errorf("into/src.txt = %q", got)
	}
}

func TestPullFile(t *testing.T) {
	c := testClient(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "guest.txt")
	write(t, src, "hello host")

	dst := filepath.Join(dir, "host.txt")
	if err := Pull(c, guestPath(src), dst, false, false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dst); got != "hello host" {
		t.Errorf("host.txt = %q", got)
	}
}

func TestPushDirectory(t *testing.T) {
	c := testClient(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "tree")
	write(t, filepath.Join(src, "a.txt"), "a")
	write(t, filepath.Join(src, "sub", "b.txt"), "b")

	dst := filepath.Join(dir, "copy")
	if err := Push(c, src, guestPath(dst), true, false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dst, "a.txt")); got != "a" {
		t.Errorf("copy/a.txt = %q", got)
	}
	if got := read(t, filepath.Join(dst, "sub", "b.txt")); got != "b" {
		t.Errorf("copy/sub/b.txt = %q", got)
	}
}

func TestPullDirectory(t *testing.T) {
	c := testClient(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "tree")
	write(t, filepath.Join(src, "a.txt"), "a")
	write(t, filepath.Join(src, "sub", "b.txt"), "b")

	dst := filepath.Join(dir, "copy")
	if err := Pull(c, guestPath(src), dst, true, false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dst, "a.txt")); got != "a" {
		t.Errorf("copy/a.txt = %q", got)
	}
	if got := read(t, filepath.Join(dst, "sub", "b.txt")); got != "b" {
		t.Errorf("copy/sub/b.txt = %q", got)
	}
}

func TestCopyRejects(t *testing.T) {
	c := testClient(t)
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	write(t, filepath.Join(tree, "a.txt"), "a")
	file := filepath.Join(dir, "f.txt")
	write(t, file, "f")

	cases := []struct {
		name    string
		run     func() error
		wantErr string
	}{
		{"push missing source", func() error {
			return Push(c, filepath.Join(dir, "nope.txt"), guestPath(filepath.Join(dir, "x")), false, false)
		}, "nope.txt"},
		{"pull missing source", func() error {
			return Pull(c, guestPath(filepath.Join(dir, "nope.txt")), filepath.Join(dir, "x"), false, false)
		}, "nope.txt"},
		{"push directory without -r", func() error {
			return Push(c, tree, guestPath(filepath.Join(dir, "x")), false, false)
		}, "pass -r"},
		{"pull directory without -r", func() error {
			return Pull(c, guestPath(tree), filepath.Join(dir, "x"), false, false)
		}, "pass -r"},
		{"push into a missing directory", func() error {
			return Push(c, file, guestPath(filepath.Join(dir, "no", "such", "f.txt")), false, false)
		}, "pass -p"},
		{"pull into a missing directory", func() error {
			return Pull(c, guestPath(file), filepath.Join(dir, "no", "such", "f.txt"), false, false)
		}, "pass -p"},
	}
	for _, tc := range cases {
		err := tc.run()
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: got %v, want it to mention %q", tc.name, err, tc.wantErr)
		}
	}
}

// -p is the opt-in that makes the same copies work: it is the only thing that
// may create directories the user did not name.
func TestCopyCreatesParentsWithFlag(t *testing.T) {
	c := testClient(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	write(t, file, "f")

	up := filepath.Join(dir, "a", "b", "f.txt")
	if err := Push(c, file, guestPath(up), false, true); err != nil {
		t.Fatal(err)
	}
	if got := read(t, up); got != "f" {
		t.Errorf("pushed with -p = %q", got)
	}

	down := filepath.Join(dir, "c", "d", "f.txt")
	if err := Pull(c, guestPath(file), down, false, true); err != nil {
		t.Fatal(err)
	}
	if got := read(t, down); got != "f" {
		t.Errorf("pulled with -p = %q", got)
	}
}
