package sshx

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"
)

// File copy runs over SFTP on an ordinary SSH connection, so it needs exactly
// what exec needs: the env's own credentials and nothing installed in the
// guest. Every OpenSSH server terrarium can already reach ships sftp-server,
// Windows included.
//
// Guest paths are always forward-slash, on every guest: SFTP paths are a
// protocol construct, not the guest's own spelling, so a Windows guest takes
// C:/Users/terrarium/x. A backslash there is a literal character in a name,
// not a separator. Hence `path` for the guest side and `filepath` for the
// host side throughout this file.

// PushTo copies a host file or directory into a guest over its SSH port.
func PushTo(port int, user, password, keyPath, local, remote string, recursive, parents bool) error {
	return withSFTP(port, user, password, keyPath, func(c *sftp.Client) error {
		return Push(c, local, remote, recursive, parents)
	})
}

// PullFrom copies a guest file or directory to the host over its SSH port.
func PullFrom(port int, user, password, keyPath, remote, local string, recursive, parents bool) error {
	return withSFTP(port, user, password, keyPath, func(c *sftp.Client) error {
		return Pull(c, remote, local, recursive, parents)
	})
}

func withSFTP(port int, user, password, keyPath string, fn func(*sftp.Client) error) error {
	conn, err := Dial(port, user, password, keyPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	c, err := sftp.NewClient(conn)
	if err != nil {
		return fmt.Errorf("opening sftp (does the guest's sshd have an sftp subsystem?): %w", err)
	}
	defer c.Close()
	return fn(c)
}

// Push copies local to remote inside the guest. Destination handling is
// scp's: a remote path that is an existing directory receives the source
// under its own name, anything else is the name to write.
func Push(c *sftp.Client, local, remote string, recursive, parents bool) error {
	fi, err := os.Stat(local)
	if err != nil {
		return err
	}
	if fi.IsDir() && !recursive {
		return fmt.Errorf("%s is a directory: pass -r to copy it", local)
	}

	dst := remote
	if remoteIsDir(c, remote) || strings.HasSuffix(remote, "/") {
		dst = path.Join(remote, filepath.Base(local))
	}
	if err := ensureRemoteParent(c, dst, parents); err != nil {
		return err
	}
	if !fi.IsDir() {
		return pushFile(c, local, dst, fi)
	}
	return pushDir(c, local, dst)
}

// Pull copies remote out of the guest to local, with the same destination
// rules as Push in the other direction.
func Pull(c *sftp.Client, remote, local string, recursive, parents bool) error {
	// Cleaned so a trailing slash cannot leave every walked path prefixed with
	// one, which would put the whole tree under an empty directory name.
	remote = path.Clean(remote)
	fi, err := c.Stat(remote)
	if err != nil {
		// The server's error names no path, and "file does not exist" alone
		// is useless when two paths are in play.
		return fmt.Errorf("%s in the guest: %w", remote, err)
	}
	if fi.IsDir() && !recursive {
		return fmt.Errorf("%s is a directory in the guest: pass -r to copy it", remote)
	}

	dst := local
	if localIsDir(local) || strings.HasSuffix(local, string(os.PathSeparator)) || strings.HasSuffix(local, "/") {
		dst = filepath.Join(local, path.Base(remote))
	}
	if err := ensureLocalParent(dst, parents); err != nil {
		return err
	}
	if !fi.IsDir() {
		return pullFile(c, remote, dst, fi)
	}
	return pullDir(c, remote, dst)
}

func pushFile(c *sftp.Client, local, remote string, fi os.FileInfo) error {
	src, err := os.Open(local)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := c.Create(remote)
	if err != nil {
		return fmt.Errorf("creating %s in the guest: %w", remote, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	// Best effort: a Windows guest has no POSIX mode to set, and losing the
	// executable bit is not a reason to fail a transfer that succeeded.
	c.Chmod(remote, fi.Mode().Perm())
	return nil
}

func pullFile(c *sftp.Client, remote, local string, fi os.FileInfo) error {
	src, err := c.Open(remote)
	if err != nil {
		return fmt.Errorf("opening %s in the guest: %w", remote, err)
	}
	defer src.Close()
	dst, err := os.Create(local)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	os.Chmod(local, fi.Mode().Perm())
	return nil
}

// pushDir walks the local tree and rebuilds it under remote. The root is
// created here, so a copy into a fresh name works without -p.
func pushDir(c *sftp.Client, local, remote string) error {
	return filepath.Walk(local, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(local, p)
		if err != nil {
			return err
		}
		target := remote
		if rel != "." {
			target = path.Join(remote, filepath.ToSlash(rel))
		}
		if fi.IsDir() {
			if err := c.MkdirAll(target); err != nil {
				return fmt.Errorf("creating %s in the guest: %w", target, err)
			}
			return nil
		}
		if !fi.Mode().IsRegular() {
			// Symlinks and devices have no meaning on the other side of a
			// guest boundary, and silently copying their target would be a lie.
			return fmt.Errorf("%s is not a regular file, skipping is not implied: remove it or copy the files individually", p)
		}
		return pushFile(c, p, target, fi)
	})
}

func pullDir(c *sftp.Client, remote, local string) error {
	if err := os.MkdirAll(local, 0o755); err != nil {
		return err
	}
	w := c.Walk(remote)
	for w.Step() {
		if err := w.Err(); err != nil {
			return err
		}
		fi := w.Stat()
		rel := strings.TrimPrefix(strings.TrimPrefix(w.Path(), remote), "/")
		target := local
		if rel != "" {
			target = filepath.Join(local, filepath.FromSlash(rel))
		}
		switch {
		case fi.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case fi.Mode().IsRegular():
			if err := pullFile(c, w.Path(), target, fi); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s in the guest is not a regular file: copy the files individually", w.Path())
		}
	}
	return nil
}

// ensureRemoteParent enforces the one rule that keeps a typo from scattering
// directories through a guest: the destination's parent has to exist already,
// unless the caller asked for it to be created.
func ensureRemoteParent(c *sftp.Client, remote string, parents bool) error {
	dir := path.Dir(remote)
	if dir == "" || dir == "." {
		return nil
	}
	if remoteIsDir(c, dir) {
		return nil
	}
	if !parents {
		return fmt.Errorf("%s does not exist in the guest: pass -p to create it", dir)
	}
	if err := c.MkdirAll(dir); err != nil {
		return fmt.Errorf("creating %s in the guest: %w", dir, err)
	}
	return nil
}

func ensureLocalParent(local string, parents bool) error {
	dir := filepath.Dir(local)
	if dir == "" || dir == "." {
		return nil
	}
	if localIsDir(dir) {
		return nil
	}
	if !parents {
		return fmt.Errorf("%s does not exist on this machine: pass -p to create it", dir)
	}
	return os.MkdirAll(dir, 0o755)
}

func remoteIsDir(c *sftp.Client, p string) bool {
	fi, err := c.Stat(p)
	return err == nil && fi.IsDir()
}

func localIsDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
