package sshx

import (
	"encoding/base64"
	"regexp"
	"strings"
	"unicode/utf16"
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

// psSafeArg matches the argv elements PowerShell's argument parser hands to
// the command unchanged. Narrower than safeArg on purpose: in argument
// position % is an alias for ForEach-Object, @ starts a splat, and a comma
// builds an array, so none of them survive a round trip bare.
var psSafeArg = regexp.MustCompile(`^[A-Za-z0-9._/:=+\\-]+$`)

// QuotePowerShell joins argv into the one command string an SSH session
// carries to a guest whose default shell is PowerShell. Single quotes are the
// only PowerShell quoting that expands nothing at all - $var, backticks and
// $(...) stay literal inside them - and a single quote is written twice to
// spell itself.
//
// A quoted first element would be a string expression rather than a command,
// so it gets the call operator: `& 'C:\Program Files\x.exe'` runs the program,
// 'C:\Program Files\x.exe' on its own just prints the path.
func QuotePowerShell(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = quotePSArg(a)
	}
	cmd := strings.Join(out, " ")
	if len(argv) > 0 && out[0] != argv[0] {
		cmd = "& " + cmd
	}
	return cmd
}

func quotePSArg(a string) string {
	if psSafeArg.MatchString(a) {
		return a
	}
	return "'" + strings.ReplaceAll(a, "'", "''") + "'"
}

// QuotePowerShellArg quotes one whole string as a single PowerShell argument.
// It is for the wrappers that build a command line by hand - `cmd /c <line>` -
// where the line is one argument to a program, not an argv PowerShell should
// re-split.
func QuotePowerShellArg(a string) string { return quotePSArg(a) }

// QuotePOSIXArg is QuotePowerShellArg for a POSIX shell.
func QuotePOSIXArg(a string) string { return quoteArg(a) }

// EncodePowerShell encodes a command for `powershell -EncodedCommand`:
// UTF-16LE, then base64. It is the only way to hand PowerShell a command
// through a shell that parses the line first and cannot be quoted out of it -
// cmd.exe splits on & and | inside every quoting it has. Base64 has neither,
// nor a space, quote or dollar, so the line survives any shell on the way.
func EncodePowerShell(command string) string {
	units := utf16.Encode([]rune(command))
	b := make([]byte, 0, len(units)*2)
	for _, u := range units {
		b = append(b, byte(u), byte(u>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}
