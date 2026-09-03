package sshx

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

// authMethods turns whatever a golden recorded into SSH auth methods.
func authMethods(password, keyPath string) ([]ssh.AuthMethod, error) {
	var auth []ssh.AuthMethod
	if keyPath != "" {
		data, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, err
		}
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("parsing key %s: %w", keyPath, err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if password != "" {
		auth = append(auth,
			ssh.Password(password),
			// some sshds only offer keyboard-interactive for passwords
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = password
				}
				return answers, nil
			}))
	}
	if len(auth) == 0 {
		return nil, fmt.Errorf("no SSH credentials: adopt the golden with --user and --password or --key")
	}
	return auth, nil
}

// Dial opens an SSH connection to a fork's forwarded port on loopback. The Go
// client rather than ssh.exe, so password auth works non-interactively.
// Host key checking is off: fork host keys rotate on every clone.
func Dial(port int, user, password, keyPath string) (*ssh.Client, error) {
	auth, err := authMethods(password, keyPath)
	if err != nil {
		return nil, err
	}
	return ssh.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port), &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	})
}
