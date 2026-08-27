//go:build !windows

package core

import "fmt"

// protectPassword needs DPAPI, which is Windows-only. Elsewhere the .rdp file
// is written without a password line and the RDP client asks for one; this
// stub exists so the rest of the package still builds.
func protectPassword(string) (string, error) {
	return "", fmt.Errorf("DPAPI password protection is only available on Windows")
}
