package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testState() *State {
	return &State{
		Goldens: map[string]*Golden{
			"ubuntu-24.04": {
				VMName:  "trr-golden-ubuntu-24.04",
				SSHUser: "terrarium",
				SSHKey:  `C:\Users\dev\AppData\Local\terrarium\goldens\ubuntu-24.04\id_ed25519`,
			},
			"centos7": {VMName: "centos7", SSHUser: "root", SSHPassword: "hunter2"},
		},
		Envs: map[string]*Env{
			"api":    {VMName: "trr-api", Golden: "ubuntu-24.04", SSHPort: 42201},
			"legacy": {VMName: "trr-legacy", Golden: "centos7", SSHPort: 42200},
		},
	}
}

func TestRenderSSHConfig(t *testing.T) {
	out := RenderSSHConfig(testState())

	// sorted, so entries do not shuffle between runs
	if strings.Index(out, "Host api") > strings.Index(out, "Host legacy") {
		t.Error("host blocks should be sorted by env name")
	}
	for _, want := range []string{
		"Host api\n",
		"  HostName 127.0.0.1\n",
		"  Port 42201\n",
		"  User terrarium\n",
		"  IdentityFile C:/Users/dev/AppData/Local/terrarium/goldens/ubuntu-24.04/id_ed25519\n",
		"  StrictHostKeyChecking no\n",
		"  UserKnownHostsFile NUL\n",
		"  LogLevel ERROR\n",
		"Host legacy\n",
		"  Port 42200\n",
		"  User root\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// A password golden has no key, so no IdentityFile.
	legacy := out[strings.Index(out, "Host legacy"):]
	if strings.Contains(legacy, "IdentityFile") {
		t.Errorf("password-only golden must not get an IdentityFile:\n%s", legacy)
	}
}

// A hostile value in state.json must not become an ssh_config directive:
// ProxyCommand there runs on the host, not in the guest.
func TestRenderSSHConfigRejectsInjection(t *testing.T) {
	st := &State{
		Goldens: map[string]*Golden{
			"evil": {
				VMName:  "evil",
				SSHUser: "root\nProxyCommand calc.exe",
				SSHKey:  "C:/k/id\nProxyCommand calc.exe",
			},
		},
		Envs: map[string]*Env{
			"pwned": {VMName: "trr-pwned", Golden: "evil", SSHPort: 42200},
		},
	}

	out := RenderSSHConfig(st)
	if strings.Contains(out, "ProxyCommand") {
		t.Errorf("injected directive reached the config:\n%s", out)
	}
	// The entry still renders, just without the poisoned fields.
	if !strings.Contains(out, "Host pwned\n") || !strings.Contains(out, "Port 42200\n") {
		t.Errorf("the rest of the block should survive:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  User ") || strings.HasPrefix(line, "  IdentityFile ") {
			t.Errorf("poisoned field should have been dropped: %q", line)
		}
	}
}

func TestRenderSSHConfigEmpty(t *testing.T) {
	st := &State{Goldens: map[string]*Golden{}, Envs: map[string]*Env{}}
	if out := RenderSSHConfig(st); out != "" {
		t.Errorf("no envs should render nothing, got %q", out)
	}
}

func TestWriteSSHConfigCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ssh", "config")

	if err := writeSSHConfig(path, RenderSSHConfig(testState())); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, sshConfigStart+"\n") {
		t.Errorf("missing start marker:\n%s", text)
	}
	if !strings.HasSuffix(text, sshConfigEnd+"\n") {
		t.Errorf("missing end marker:\n%s", text)
	}
	if !strings.Contains(text, "Host api") {
		t.Errorf("missing host block:\n%s", text)
	}
}

func TestWriteSSHConfigPreservesSurroundings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	before := "Host github.com\n  User git\n  IdentityFile ~/.ssh/id_rsa\n\n"
	after := "\nHost prod\n  HostName prod.example.com\n"
	original := before + sshConfigStart + "\nHost stale\n  Port 1\n" + sshConfigEnd + "\n" + after
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writeSSHConfig(path, RenderSSHConfig(testState())); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)

	if !strings.HasPrefix(text, before) {
		t.Errorf("content before the markers was modified:\n%s", text)
	}
	if !strings.HasSuffix(text, after) {
		t.Errorf("content after the markers was modified:\n%s", text)
	}
	if strings.Contains(text, "Host stale") {
		t.Errorf("old managed content survived:\n%s", text)
	}
	if !strings.Contains(text, "Host api") {
		t.Errorf("new managed content missing:\n%s", text)
	}
	if strings.Count(text, sshConfigStart) != 1 || strings.Count(text, sshConfigEnd) != 1 {
		t.Errorf("markers were duplicated:\n%s", text)
	}
}

func TestWriteSSHConfigAppendsToExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	// no trailing newline: the marker must not land on the user's last line
	if err := os.WriteFile(path, []byte("Host github.com\n  User git"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writeSSHConfig(path, RenderSSHConfig(testState())); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "  User git\n"+sshConfigStart) {
		t.Errorf("marker should start its own line:\n%s", data)
	}
}

func TestWriteSSHConfigEmptySection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	st := &State{Goldens: map[string]*Golden{}, Envs: map[string]*Env{}}

	if err := writeSSHConfig(path, RenderSSHConfig(st)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != sshConfigStart+"\n"+sshConfigEnd+"\n" {
		t.Errorf("unexpected empty section:\n%q", data)
	}
}

func TestWriteSSHConfigMalformedMarkers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("Host a\n"+sshConfigStart+"\nHost b\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writeSSHConfig(path, ""); err == nil {
		t.Error("a start marker with no end marker must be an error, not a second section")
	}
}

func TestUpdateSSHConfigUsesProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)

	if err := UpdateSSHConfig(testState()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Host api") {
		t.Errorf("unexpected config:\n%s", data)
	}
}

// A password-only env's block otherwise looks like every other one, so `scp
// <env>:` pops an askpass dialog with nothing in it to say where it came
// from. The block has to admit what it is.
func TestRenderSSHConfigMarksPasswordAuth(t *testing.T) {
	out := RenderSSHConfig(testState())

	legacy := out[strings.Index(out, "Host legacy"):]
	if !strings.Contains(legacy, "  "+passwordAuthNote+"\n") {
		t.Errorf("a password-only golden's block should say so:\n%s", legacy)
	}
	// The note is a comment, so ssh reads past it: nothing in the block may
	// look like a directive.
	if !strings.Contains(legacy, "  #") {
		t.Error("the note must be a comment, or ssh will reject the config")
	}
	// A key-based env has nothing to warn about and must not be cluttered.
	api := out[strings.Index(out, "Host api"):strings.Index(out, "Host legacy")]
	if strings.Contains(api, passwordAuthNote) {
		t.Errorf("a key golden's block should not carry the note:\n%s", api)
	}
}

// Which goldens the CLI names when it rewrites the config.
func TestPasswordAuthGoldens(t *testing.T) {
	st := testState()
	if got := PasswordAuthGoldens(st); len(got) != 1 || got[0] != "centos7" {
		t.Errorf("got %v, want [centos7]", got)
	}
	// A golden with both is key-based; the password is there for RDP and the
	// console, which is what a Windows golden looks like now.
	st.Goldens["centos7"].SSHKey = `C:\keys\centos7`
	if got := PasswordAuthGoldens(st); len(got) != 0 {
		t.Errorf("a golden with a key needs no warning, got %v", got)
	}
	// Nothing is forked from it, so nothing in the config mentions it.
	st.Goldens["unused"] = &Golden{VMName: "unused", SSHUser: "root", SSHPassword: "x"}
	if got := PasswordAuthGoldens(st); len(got) != 0 {
		t.Errorf("a golden with no envs is not in the config, got %v", got)
	}
}
