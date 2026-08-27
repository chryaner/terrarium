package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderRDPWithPassword(t *testing.T) {
	got := renderRDP("127.0.0.1:42210", "terrarium", "01000000D08C9DDF")

	want := "full address:s:127.0.0.1:42210\r\n" +
		"username:s:terrarium\r\n" +
		"password 51:b:01000000D08C9DDF\r\n" +
		"authentication level:i:0\r\n" +
		"prompt for credentials:i:0\r\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// Without a blob the line is absent, not empty: `password 51:b:` with nothing
// after it makes mstsc fail rather than prompt.
func TestRenderRDPWithoutPassword(t *testing.T) {
	got := renderRDP("127.0.0.1:42210", "Administrator", "")

	want := "full address:s:127.0.0.1:42210\r\n" +
		"username:s:Administrator\r\n" +
		"authentication level:i:0\r\n" +
		"prompt for credentials:i:0\r\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
	if strings.Contains(got, "password") {
		t.Errorf("no password line should be written:\n%s", got)
	}
}

func TestRenderRDPUsesCRLF(t *testing.T) {
	got := renderRDP("127.0.0.1:1", "u", "AB")
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Errorf("every line ending must be CRLF: %q", got)
	}
	if !strings.HasSuffix(got, "\r\n") {
		t.Errorf("file should end with a line ending: %q", got)
	}
}

func rdpTestEngine(t *testing.T, g *Golden) *Engine {
	t.Helper()
	t.Setenv("LOCALAPPDATA", t.TempDir())
	st, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	st.Goldens["win11"] = g
	st.Envs["desk"] = &Env{VMName: "trr-desk", Golden: "win11", SSHPort: 42200, Created: time.Now()}
	// VB is unused here: writing the file touches no VM.
	return &Engine{St: st}
}

func TestWriteRDPFile(t *testing.T) {
	e := rdpTestEngine(t, &Golden{VMName: "win11", SSHUser: "terrarium", SSHPassword: "Terrarium1!"})

	path, err := e.WriteRDPFile("desk", 42210)
	if err != nil {
		t.Fatal(err)
	}
	// Resolved through the shared data dir, so LOCALAPPDATA moves it.
	dir, err := dataDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "rdp", "desk.rdp"); path != want {
		t.Errorf("path: got %q, want %q", path, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "full address:s:127.0.0.1:42210\r\n") {
		t.Errorf("address missing:\n%s", text)
	}
	if !strings.Contains(text, "username:s:terrarium\r\n") {
		t.Errorf("username missing:\n%s", text)
	}
	// The password is never written in the clear, whatever the platform did
	// with the blob.
	if strings.Contains(text, "Terrarium1!") {
		t.Errorf("plaintext password leaked into the file:\n%s", text)
	}
}

// A golden with no password still gets a useful file: address and username
// filled in, one prompt to go.
func TestWriteRDPFileWithoutPassword(t *testing.T) {
	e := rdpTestEngine(t, &Golden{VMName: "win11", SSHUser: "terrarium", SSHKey: `C:\k\id_ed25519`})

	path, err := e.WriteRDPFile("desk", 42210)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "password 51:b:") {
		t.Errorf("no password line expected:\n%s", data)
	}
}

func TestWriteRDPFileUnknownEnv(t *testing.T) {
	e := rdpTestEngine(t, &Golden{VMName: "win11", SSHUser: "terrarium"})

	if _, err := e.WriteRDPFile("nope", 42210); err == nil {
		t.Error("expected an error for an unknown env")
	}
}

func TestRemoveRDPFile(t *testing.T) {
	e := rdpTestEngine(t, &Golden{VMName: "win11", SSHUser: "terrarium", SSHPassword: "pw"})

	path, err := e.WriteRDPFile("desk", 42210)
	if err != nil {
		t.Fatal(err)
	}
	removeRDPFile("desk")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be gone, stat gave %v", err)
	}
	// Removing what is not there is not an error.
	removeRDPFile("desk")
}
