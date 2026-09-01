package sshx

import (
	"regexp"
	"strings"
)

// safeArg matches the argv elements a POSIX shell hands on untouched. Those
// cross the wire bare, so an ordinary `uname -a` still reads as itself in a
// timeout error and still means the same thing to a cmd.exe guest.
var safeArg = regexp.MustCompile(`^[A-Za-z0-9._/=:,+@%-]+$`)

// QuotePOSIX joins argv into the single command string an SSH session carries,
// quoting each element so the guest's shell rebuilds the exact argv. Without
// it the guest re-splits arguments on spaces and reads |, $, backticks and
// globs inside them as its own syntax: `bash -c 'a; b'` word-splits and the |
// in a sed expression becomes a remote pipe. Shell features are still
// available, they just have to be asked for - as an argument to `bash -c`
// rather than by accident.
func QuotePOSIX(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = quoteArg(a)
	}
	return strings.Join(out, " ")
}

// quoteArg wraps one element in single quotes, the one POSIX quoting that
// suppresses everything, including the backslash. A single quote cannot
// appear inside them at all, so it is spelled by leaving the quoting,
// escaping one quote with a backslash, and starting again.
func quoteArg(a string) string {
	if safeArg.MatchString(a) {
		return a
	}
	return "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
}
