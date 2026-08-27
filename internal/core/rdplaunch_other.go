//go:build !windows

package core

import "fmt"

// Credential Manager and mstsc's certificate store are Windows-only. These
// stubs exist so the package builds elsewhere; the CLI never reaches them,
// because it only launches mstsc on Windows.

func storeRDPCredential(target, user, password string) error {
	return fmt.Errorf("Credential Manager is only available on Windows")
}

func deleteRDPCredential(target string) {}

func trustRDPCert(host string, port int) error {
	return fmt.Errorf("mstsc certificate trust is only available on Windows")
}
