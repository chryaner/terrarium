// Package seed generates what a stock cloud image needs before it is
// reachable: an SSH keypair and a cloud-init NoCloud seed ISO carrying the
// public half. Both are produced in-process - no ssh-keygen, no
// genisoimage/mkisofs, nothing for the user to install.
package seed

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kdomanski/iso9660"
	"golang.org/x/crypto/ssh"
)

// User is the account cloud-init creates in the guest.
const User = "terrarium"

const (
	keyFile = "id_ed25519"
	isoFile = "seed.iso"
	// cloud-init's NoCloud datasource only looks at volumes labelled cidata.
	volumeLabel = "cidata"
)

// DefaultPackages is what an image gets when its recipe says nothing.
var DefaultPackages = []string{"virtualbox-guest-utils"}

// Generate writes the keypair and seed ISO for image under baseDir/<image>/.
// The key is reused if it already exists - regenerating it would lock us out
// of goldens built earlier - while the ISO is always rewritten.
//
// A nil packages list means DefaultPackages; an empty one means install
// nothing, for distributions that ship no guest additions package.
func Generate(baseDir, image string, packages []string) (keyPath, isoPath string, err error) {
	if packages == nil {
		packages = DefaultPackages
	}
	// Package names are pasted into a #cloud-config document that runs as root
	// on first boot, so a line break in one injects arbitrary directives. The
	// recipe loader rejects these too; this is the layer that cannot be
	// bypassed by whatever else learns to call Generate.
	for _, p := range packages {
		if strings.ContainsAny(p, "\r\n") {
			return "", "", fmt.Errorf("package %q contains a line break", p)
		}
	}
	dir := filepath.Join(baseDir, image)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	keyPath = filepath.Join(dir, keyFile)
	pubKey, err := ensureKey(keyPath)
	if err != nil {
		return "", "", err
	}
	isoPath = filepath.Join(dir, isoFile)
	if err := writeISO(isoPath, userData(pubKey, packages), metaData(image)); err != nil {
		return "", "", err
	}
	return keyPath, isoPath, nil
}

// ensureKey returns the authorized_keys line for the key at path, generating
// the pair first if it is not there yet.
func ensureKey(path string) (string, error) {
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
	block, err := ssh.MarshalPrivateKey(priv, User)
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

// shareMount is the guest half of `terrarium up`'s shared folder. The vboxsf
// driver is mainline, but the mount helper ships in the guest additions
// package, so the mount only works on images whose recipe installs it. It is
// written unconditionally anyway: uid/gid 1000 is the terrarium user, and
// nofail plus x-systemd.automount keep boot clean both on forks with no share
// attached and on images that never get the helper.
const shareMount = `runcmd:
  - mkdir -p /work
  - printf 'work /work vboxsf uid=1000,gid=1000,nofail,x-systemd.automount 0 0\n' >> /etc/fstab
`

func userData(pubKey string, packages []string) string {
	var b strings.Builder
	b.WriteString(`#cloud-config
users:
  - name: ` + User + `
    ssh_authorized_keys:
      - ` + pubKey + `
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    groups: sudo
ssh_pwauth: false
`)
	if len(packages) > 0 {
		b.WriteString("packages:\n")
		for _, p := range packages {
			b.WriteString("  - " + p + "\n")
		}
	}
	b.WriteString(shareMount)
	return b.String()
}

func metaData(image string) string {
	return "instance-id: terrarium-" + image + "\n" +
		"local-hostname: " + strings.ReplaceAll(image, ".", "-") + "\n"
}

func writeISO(path, userData, metaData string) error {
	w, err := iso9660.NewWriter()
	if err != nil {
		return err
	}
	defer w.Cleanup()

	if err := w.AddFile(strings.NewReader(userData), "user-data"); err != nil {
		return err
	}
	if err := w.AddFile(strings.NewReader(metaData), "meta-data"); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := w.WriteTo(f, volumeLabel); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
