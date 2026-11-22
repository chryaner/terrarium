package core

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/chryaner/terrarium/internal/paths"
)

const (
	portClaimDir = "ports"
	// portClaimTTL covers the gap between choosing a port and the moment
	// something else holds it: the env's record reaching the state file, or
	// the booted VM listening on it. Every caller closes that gap in seconds.
	portClaimTTL = 2 * time.Minute
)

// freePort picks a host port for a NAT forward and claims it, so two terrarium
// processes starting at the same moment cannot both be handed the same one.
// A port is taken if an env on disk or in this process records it, if
// something is listening on it, or if another caller claimed it within
// portClaimTTL. The state file is read again here because a long-lived process
// such as the MCP server would otherwise not see the ports of envs made, and
// since shut down, by others.
func (e *Engine) freePort() (int, error) {
	used := map[int]bool{}
	mark := func(s *State) {
		for _, env := range s.Envs {
			used[env.SSHPort] = true
			used[env.RDPPort] = true
		}
	}
	mark(e.St)

	claims := ""
	if e.St.path != "" {
		dir := filepath.Dir(e.St.path)
		if err := paths.MakePrivate(dir); err != nil {
			return 0, err
		}
		unlock, err := lockState(filepath.Join(dir, lockFileName), lockTimeout)
		if err != nil {
			return 0, err
		}
		defer unlock()

		disk, err := readStateFile(e.St.path)
		if err != nil {
			return 0, err
		}
		mark(disk)
		claims = filepath.Join(dir, portClaimDir)
		if err := os.MkdirAll(claims, 0o700); err != nil {
			return 0, err
		}
	}

	for p := portRangeStart; p < portRangeEnd; p++ {
		if used[p] {
			continue
		}
		if claims != "" && portClaimed(claims, p) {
			continue
		}
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err != nil {
			continue
		}
		l.Close()
		if claims != "" {
			if err := os.WriteFile(filepath.Join(claims, strconv.Itoa(p)), nil, 0o600); err != nil {
				return 0, err
			}
		}
		return p, nil
	}
	return 0, fmt.Errorf("no free port in %d-%d", portRangeStart, portRangeEnd)
}

// portClaimed reports whether a claim on the port is still in force. A claim
// past its TTL has done its job and is removed. Callers hold the state lock.
func portClaimed(dir string, port int) bool {
	path := filepath.Join(dir, strconv.Itoa(port))
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if time.Since(info.ModTime()) < portClaimTTL {
		return true
	}
	os.Remove(path)
	return false
}
