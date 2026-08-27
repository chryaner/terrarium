package core

import "unicode"

// singleLine reports whether s can be written as part of one line of a config
// file without changing its meaning.
//
// RenderSSHConfig writes recorded values into the user's real ~/.ssh/config,
// where a newline turns the rest of a value into further ssh_config
// directives - and a ProxyCommand there runs on the host, not in the guest.
// The values reach us from state.json and from recipe files, neither of which
// is necessarily written by someone trustworthy.
func singleLine(s string) bool {
	for _, r := range s {
		if r == '\r' || r == '\n' || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// validSSHUser is checked wherever an SSH user name is recorded.
func validSSHUser(s string) bool { return singleLine(s) }
