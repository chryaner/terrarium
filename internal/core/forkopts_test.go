package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckForkHostname(t *testing.T) {
	linux := &Golden{OSType: "Ubuntu_64", SSHUser: "terrarium", SSHKey: "k"}
	win := &Golden{OSType: "Windows11_64", SSHUser: "terrarium", SSHKey: "k"}
	cases := []struct {
		name    string
		host    string
		g       *Golden
		wantErr string
	}{
		{name: "none asked for", host: "", g: &Golden{}},
		{name: "plain", host: "agent1", g: linux},
		{name: "dashes and digits", host: "ci-runner-07", g: linux},
		{name: "63 characters", host: strings.Repeat("a", 63), g: linux},
		{name: "64 characters", host: strings.Repeat("a", 64), g: linux, wantErr: "invalid hostname"},
		{name: "leading dash", host: "-agent", g: linux, wantErr: "invalid hostname"},
		{name: "trailing dash", host: "agent-", g: linux, wantErr: "invalid hostname"},
		{name: "dot", host: "a.b", g: linux, wantErr: "invalid hostname"},
		{name: "shell metacharacter", host: "a;reboot", g: linux, wantErr: "invalid hostname"},
		{name: "quote", host: "a'b", g: linux, wantErr: "invalid hostname"},
		{name: "space", host: "a b", g: linux, wantErr: "invalid hostname"},
		{name: "windows 15 characters", host: strings.Repeat("a", 15), g: win},
		{name: "windows 16 characters", host: strings.Repeat("a", 16), g: win, wantErr: "too long for Windows"},
		{name: "windows all digits", host: "12345", g: win, wantErr: "all digits"},
		{name: "linux all digits", host: "12345", g: linux},
		{name: "windows xp", host: "box", g: &Golden{OSType: "WindowsXP"}, wantErr: "no PowerShell"},
		{name: "no login recorded", host: "box", g: &Golden{OSType: "Ubuntu_64"}, wantErr: "records no login"},
		{
			name: "guestcontrol without a password", host: "box",
			g:       &Golden{OSType: "Windows10_64", Transport: TransportGuestControl, SSHUser: "u"},
			wantErr: "records no login",
		},
		{
			name: "guestcontrol with a password", host: "box",
			g: &Golden{OSType: "Windows10_64", Transport: TransportGuestControl, SSHUser: "u", SSHPassword: "p"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkForkHostname(c.host, c.g)
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Fatalf("want error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}

func TestSerialLogFor(t *testing.T) {
	cfg := filepath.Join("vms", "trr-a", "trr-a.vbox")
	want := filepath.Join("vms", "trr-a", serialLogName)
	cases := []struct {
		name   string
		info   map[string]string
		ostype string
		want   string
	}{
		{name: "linux with the port off gets one", info: map[string]string{"CfgFile": cfg, "uart1": "off"}, ostype: "Debian_64", want: want},
		{name: "unknown guest with the port off gets one", info: map[string]string{"CfgFile": cfg, "uart1": "off"}, want: want},
		{name: "windows with the port off is left alone", info: map[string]string{"CfgFile": cfg, "uart1": "off"}, ostype: "Windows10_64"},
		{
			name:   "the golden's log file is replaced by the fork's own",
			info:   map[string]string{"CfgFile": cfg, "uart1": "0x03f8,4", "uartmode1": `file,C:\vms\golden\serial.log`},
			ostype: "Windows10_64", want: want,
		},
		{name: "a disconnected port is put to use", info: map[string]string{"CfgFile": cfg, "uart1": "0x03f8,4", "uartmode1": "disconnected"}, ostype: "Debian_64", want: want},
		{name: "a pipe someone set up is kept", info: map[string]string{"CfgFile": cfg, "uart1": "0x03f8,4", "uartmode1": `server,\\.\pipe\dbg`}, ostype: "Debian_64"},
		{name: "a tcp server is kept", info: map[string]string{"CfgFile": cfg, "uart1": "0x03f8,4", "uartmode1": "tcpserver,2001"}, ostype: "Debian_64"},
		{name: "no settings file known", info: map[string]string{"uart1": "off"}, ostype: "Debian_64"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := serialLogFor(c.info, c.ostype); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSerialLogOf(t *testing.T) {
	cases := []struct {
		name string
		info map[string]string
		want string
	}{
		{name: "file", info: map[string]string{"uart1": "0x03f8,4", "uartmode1": `file,C:\vms\a\serial.log`}, want: `C:\vms\a\serial.log`},
		{name: "comma in the path", info: map[string]string{"uart1": "0x03f8,4", "uartmode1": "file,/vms/a,b/serial.log"}, want: "/vms/a,b/serial.log"},
		{name: "port off", info: map[string]string{"uart1": "off", "uartmode1": "file,/x"}},
		{name: "pipe", info: map[string]string{"uart1": "0x03f8,4", "uartmode1": "server,/tmp/pipe"}},
		{name: "nothing reported", info: map[string]string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := serialLogOf(c.info); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestConsoleTail(t *testing.T) {
	log := "first\r\n\r\n\x1b[0;32m  OK  \x1b[0m Started sshd\r\n" +
		"[    2.44] e1000\teth0\x07\r\n" +
		strings.Repeat("x", 300) + "\n" +
		"Begin: Loading essential drivers ...   \n"
	got := consoleTail([]byte(log), 3, 20)
	want := []string{"[    2.44] e1000 eth", strings.Repeat("x", 20), "Begin: Loading essen"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q, want %q", i, got[i], want[i])
		}
	}

	if got := consoleTail([]byte("\x1b[2J\r\n \r\n"), 5, 80); len(got) != 0 {
		t.Errorf("a log of control codes and blanks has no lines, got %q", got)
	}
	if got := consoleTail([]byte("  OK  \x1b[1;31mfailed\x1b[0m"), 5, 80); len(got) != 1 || got[0] != "  OK  failed" {
		t.Errorf("colour codes should vanish and indentation stay, got %q", got)
	}
}

func TestStallWatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), serialLogName)
	start := time.Now()
	w := stallWatch{path: path, limit: time.Minute}

	if w.check(start) {
		t.Fatal("a log that does not exist yet is not a stall")
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	w.check(start)
	if w.check(start.Add(10 * time.Minute)) {
		t.Fatal("an empty log says nothing about the guest: not a stall")
	}

	if err := os.WriteFile(path, []byte("booting\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t1 := start.Add(11 * time.Minute)
	if w.check(t1) {
		t.Fatal("the log just grew")
	}
	if w.check(t1.Add(59 * time.Second)) {
		t.Fatal("silent for less than the limit")
	}
	if !w.check(t1.Add(time.Minute)) {
		t.Fatal("silent for the whole limit: stalled")
	}

	if err := os.WriteFile(path, []byte("booting\nmore\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if w.check(t1.Add(2 * time.Minute)) {
		t.Fatal("growth resets the clock")
	}

	none := stallWatch{limit: time.Minute}
	if none.check(start) || none.check(start.Add(time.Hour)) {
		t.Fatal("no log to watch is never a stall")
	}
}

func TestPosixHostnameScriptNamesEveryPlace(t *testing.T) {
	script := posixHostnameScript("agent1")
	for _, want := range []string{"hostnamectl set-hostname agent1", "/etc/hostname", "127.0.1.1 agent1", "sudo -n"} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
}

func TestAtLoginPrompt(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	booted := strings.Repeat("kernel line\n", 1000) + "Ubuntu 24.04 LTS ubuntu ttyS0\n\nubuntu login: "
	hung := strings.Repeat("kernel line\n", 1000) + "Begin: Loading essential drivers ... "
	// A prompt from before the last reset, far above the end of the log.
	stale := "debian login: \n" + strings.Repeat("kernel line\n", 1000)

	if !atLoginPrompt(write("booted", booted)) {
		t.Error("a console ending at a login prompt is a booted guest")
	}
	if atLoginPrompt(write("hung", hung)) {
		t.Error("a console that stops in the initramfs is not at a login prompt")
	}
	if atLoginPrompt(write("stale", stale)) {
		t.Error("an old prompt far up the log says nothing about this boot")
	}
	if !atLoginPrompt(write("short", "login: ")) {
		t.Error("a log shorter than the tail window is read whole")
	}
	if atLoginPrompt(filepath.Join(dir, "absent")) || atLoginPrompt("") {
		t.Error("no log, no prompt")
	}
}
