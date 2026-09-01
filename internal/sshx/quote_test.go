package sshx

import (
	"strings"
	"testing"
)

// What the guest shell must see for it to rebuild the argv the user typed.
// The field report: `bash -c '...'` payloads word-split and a | inside a sed
// expression ran as a remote pipe.
func TestQuotePOSIX(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want string
	}{
		{"ordinary words are left alone", []string{"uname", "-a"}, "uname -a"},
		{"a space must not split the argument", []string{"cat", "my file.txt"}, `cat 'my file.txt'`},
		{"a pipe belongs to the argument, not the shell", []string{"grep", "a|b"}, `grep 'a|b'`},
		{"the guest must not expand variables", []string{"echo", "$HOME"}, `echo '$HOME'`},
		{"backticks must not run in the guest", []string{"echo", "`id`"}, "echo '`id`'"},
		{"a glob reaches the command unexpanded", []string{"ls", "*.go"}, `ls '*.go'`},
		{"an empty argument survives as an argument", []string{"test", "-z", ""}, `test -z ''`},
		// Single quotes cannot nest, so the quoting closes, escapes and reopens.
		{"embedded single quote", []string{"echo", "it's"}, `echo 'it'\''s'`},
		{"only a quote", []string{"echo", "'"}, `echo ''\'''`},
		// The two cases from the report, spelled the way they should have
		// worked all along: @ delimiters were a workaround for the join.
		{"sed expression", []string{"sed", "-e", "s@a@b@", "f.txt"}, "sed -e s@a@b@ f.txt"},
		{"sed with the usual delimiter and a pipe", []string{"sed", "-e", `s|a|b|`}, `sed -e 's|a|b|'`},
		{"a shell payload stays one argument", []string{"bash", "-c", "x; y"}, `bash -c 'x; y'`},
		{"nothing to run", nil, ""},
	}
	for _, c := range cases {
		if got := QuotePOSIX(c.argv); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// cli/exec.go decides a guest is Windows only when quoting changed the string,
// so argv with nothing for a shell to eat has to come out byte-identical to a
// plain join - otherwise every exec pays for a VBoxManage call.
func TestQuotePOSIXMatchesPlainJoinWhenNothingNeedsQuoting(t *testing.T) {
	argv := []string{"sudo", "systemctl", "restart", "nginx.service", "--now"}
	if got, want := QuotePOSIX(argv), strings.Join(argv, " "); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A Windows path is not a POSIX-safe argument: the backslashes would be eaten
// on the way to a Linux guest, so it has to be quoted there. (The Windows
// guest never gets the quoted form - see cli/exec.go.)
func TestQuotePOSIXQuotesBackslashes(t *testing.T) {
	if got, want := QuotePOSIX([]string{"printf", `a\tb`}), `printf 'a\tb'`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
