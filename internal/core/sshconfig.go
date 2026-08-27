package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Everything between these markers belongs to terrarium and is rewritten
// wholesale; everything outside them is the user's and is never touched.
const (
	sshConfigStart = "# >>> terrarium managed >>>"
	sshConfigEnd   = "# <<< terrarium managed <<<"
)

func sshConfigPath() (string, error) {
	home := os.Getenv("USERPROFILE")
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

// RenderSSHConfig builds one Host block per env, so VS Code Remote-SSH and
// plain `ssh <env>` both work. Stopped envs are included: the host entry is
// what the user picks from before starting one.
func RenderSSHConfig(st *State) string {
	names := make([]string, 0, len(st.Envs))
	for name := range st.Envs {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		env := st.Envs[name]
		g := st.Goldens[env.Golden]

		fmt.Fprintf(&b, "Host %s\n", name)
		fmt.Fprintf(&b, "  HostName 127.0.0.1\n")
		fmt.Fprintf(&b, "  Port %d\n", env.SSHPort)
		// Last line of defence: this writes into the user's real ssh config,
		// where a newline in a value becomes a directive of its own and a
		// ProxyCommand runs on the host. Values are validated where they are
		// recorded; a bad one that got in anyway is dropped rather than written.
		if g != nil && g.SSHUser != "" && singleLine(g.SSHUser) {
			fmt.Fprintf(&b, "  User %s\n", g.SSHUser)
		}
		if g != nil && g.SSHKey != "" && singleLine(g.SSHKey) {
			// ssh reads backslashes as escapes even on Windows. Explicit
			// replace, not filepath.ToSlash: behavior must not depend on the
			// build OS (CI runs these tests on Linux).
			fmt.Fprintf(&b, "  IdentityFile %s\n", strings.ReplaceAll(g.SSHKey, `\`, "/"))
		}
		fmt.Fprintf(&b, "  StrictHostKeyChecking no\n")
		fmt.Fprintf(&b, "  UserKnownHostsFile NUL\n")
		fmt.Fprintf(&b, "  LogLevel ERROR\n")
	}
	return b.String()
}

// UpdateSSHConfig rewrites the managed section of the user's ssh config.
func UpdateSSHConfig(st *State) error {
	path, err := sshConfigPath()
	if err != nil {
		return err
	}
	return writeSSHConfig(path, RenderSSHConfig(st))
}

func writeSSHConfig(path, section string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	managed := sshConfigStart + "\n" + section + sshConfigEnd + "\n"
	text := string(old)
	start := strings.Index(text, sshConfigStart)
	end := strings.Index(text, sshConfigEnd)

	switch {
	case start < 0 && end < 0:
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += managed
	case start >= 0 && end > start:
		end += len(sshConfigEnd)
		// take the marker's own line ending with it
		text = text[:start] + managed + strings.TrimPrefix(strings.TrimPrefix(text[end:], "\r"), "\n")
	default:
		// Half a section means someone edited inside our markers. Rewriting
		// would either duplicate or eat their content.
		return fmt.Errorf("%s: terrarium markers are malformed, fix or remove them", path)
	}
	return os.WriteFile(path, []byte(text), 0o600)
}
