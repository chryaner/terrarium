// Package sshx: SSH readiness probing and client invocation for forks.
package sshx

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// WaitReady returns once sshd answers with its banner on 127.0.0.1:port.
// A TCP connect alone is not enough: VirtualBox NAT accepts host connections
// before the guest is listening.
func WaitReady(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second)
		if err == nil {
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			buf := make([]byte, 64)
			n, _ := conn.Read(buf)
			conn.Close()
			if n > 0 && strings.HasPrefix(string(buf[:n]), "SSH-") {
				return nil
			}
		}
		time.Sleep(700 * time.Millisecond)
	}
	return fmt.Errorf("no SSH banner on 127.0.0.1:%d after %s", port, timeout)
}

// Args builds ssh client arguments for a fork. Host keys rotate with every
// fork, so strict checking and known-hosts persistence are disabled.
// UserKnownHostsFile=NUL is the Windows null device (host is Windows-only
// for now).
func Args(port int, user, key string) []string {
	a := []string{
		"-p", strconv.Itoa(port),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=NUL",
		"-o", "LogLevel=ERROR",
	}
	if key != "" {
		a = append(a, "-i", key)
	}
	return append(a, user+"@127.0.0.1")
}
