// Package seed generates what a stock cloud image needs before it is
// reachable: an SSH keypair and a cloud-init NoCloud seed ISO carrying the
// public half. Both are produced in-process - no ssh-keygen, no
// genisoimage/mkisofs, nothing for the user to install.
package seed

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chryaner/terrarium/internal/sshx"
	"github.com/kdomanski/iso9660"
)

// User is the account cloud-init creates in the guest.
const User = "terrarium"

const (
	keyFile = "id_ed25519"
	isoFile = "seed.iso"
	// cloud-init's NoCloud datasource only looks at volumes labelled cidata.
	volumeLabel = "cidata"
)

// KeyPath is where an image's private key lives. The Windows install path
// builds no seed ISO but shares the layout, so both find the key the same way.
func KeyPath(baseDir, image string) string {
	return filepath.Join(baseDir, image, keyFile)
}

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
	keyPath = KeyPath(baseDir, image)
	// The generator is shared with the Windows install path, which installs
	// the same kind of key by a different route.
	pubKey, err := sshx.EnsureKey(keyPath, User)
	if err != nil {
		return "", "", err
	}
	isoPath = filepath.Join(dir, isoFile)
	if err := writeISO(isoPath, userData(pubKey, packages), metaData(image)); err != nil {
		return "", "", err
	}
	return keyPath, isoPath, nil
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
