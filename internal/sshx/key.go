package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
)

// EnsureKey returns the authorized_keys line for the ed25519 key at path,
// generating the pair and writing path and path+".pub" if it is not there
// yet. An existing key is reused rather than regenerated: a fresh one would
// lock terrarium out of the goldens the old one was installed in.
//
// Both the cloud-init seed and the Windows unattended install put the public
// half in the guest this way, so they share the generator: every golden
// terrarium builds ends up key-based, whatever the OS.
func EnsureKey(path, comment string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return "", fmt.Errorf("parsing existing key %s: %w", path, err)
		}
		return authorizedKey(signer.PublicKey()), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", err
	}
	// 0600 is advisory on Windows; the file lands in LOCALAPPDATA either way.
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return "", err
	}
	line := authorizedKey(sshPub)
	if err := os.WriteFile(path+".pub", []byte(line+"\n"), 0o644); err != nil {
		return "", err
	}
	return line, nil
}

func authorizedKey(pub ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
}
