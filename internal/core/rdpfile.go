package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// renderRDP builds an .rdp file. Lines are CRLF, matching what Windows writes
// itself.
//
// `authentication level:i:0` stops mstsc warning about the server
// certificate. The guest presents a self-signed one it generated, and the
// connection is a loopback port forward into a throwaway VM, so there is no
// man in the middle for the warning to protect against.
func renderRDP(fullAddress, username, passwordBlobHex string) string {
	lines := []string{
		"full address:s:" + fullAddress,
		"username:s:" + username,
	}
	if passwordBlobHex != "" {
		lines = append(lines, "password 51:b:"+passwordBlobHex)
	}
	lines = append(lines,
		"authentication level:i:0",
		"prompt for credentials:i:0",
	)
	return strings.Join(lines, "\r\n") + "\r\n"
}

func rdpFilePath(envName string) (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "rdp", envName+".rdp"), nil
}

// WriteRDPFile writes an .rdp file that connects without prompting: address,
// username, and the password as the same DPAPI blob mstsc's "Remember me"
// stores. A password that cannot be protected is left out rather than written
// in the clear - the file still fills in everything else, so the user types
// only the password.
func (e *Engine) WriteRDPFile(envName string, hostPort int) (string, error) {
	_, user, password, _, err := e.SSHTarget(envName)
	if err != nil {
		return "", err
	}
	var blob string
	if password != "" {
		// Failing here costs a prompt, not the connection.
		blob, _ = protectPassword(password)
	}

	path, err := rdpFilePath(envName)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	content := renderRDP(fmt.Sprintf("127.0.0.1:%d", hostPort), user, blob)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// removeRDPFile drops the generated file when an env goes away. Best effort:
// a leftover file is harmless, and failing `rm` over one would not be.
func removeRDPFile(envName string) {
	if path, err := rdpFilePath(envName); err == nil {
		os.Remove(path)
	}
}
