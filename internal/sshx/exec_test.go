package sshx

import (
	"slices"
	"strings"
	"testing"
)

func TestArgs(t *testing.T) {
	got := Args(42200, "terrarium", `C:\keys\id_ed25519`)
	want := []string{
		"-p", "42200",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=NUL",
		"-o", "LogLevel=ERROR",
		"-i", `C:\keys\id_ed25519`,
		"terrarium@127.0.0.1",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

// Password goldens have no key, and `-i ""` is not the same as no -i.
func TestArgsWithoutKey(t *testing.T) {
	got := Args(42201, "root", "")
	for _, a := range got {
		if a == "-i" {
			t.Errorf("-i should be omitted when there is no key: %v", got)
		}
	}
	if got[len(got)-1] != "root@127.0.0.1" {
		t.Errorf("target should come last, got %v", got)
	}
}

func TestTimeoutErrorSaysItIsStillRunning(t *testing.T) {
	err := &TimeoutError{Timeout: 5, Command: "make test"}
	msg := err.Error()
	if !strings.Contains(msg, "make test") {
		t.Errorf("error should quote the command: %s", msg)
	}
	if !strings.Contains(msg, "still running") {
		t.Errorf("error should say the guest is still working: %s", msg)
	}
}

func TestOutputBufferCollects(t *testing.T) {
	var b OutputBuffer
	b.Write([]byte("hello "))
	b.Write([]byte("world"))
	if got := b.String(); got != "hello world" {
		t.Errorf("got %q", got)
	}
}
