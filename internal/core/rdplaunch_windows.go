//go:build windows

package core

import (
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"runtime"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The Credential Manager API is not wrapped by x/sys/windows, so it is
// declared here. Layout and constants come from wincred.h.
var (
	modadvapi32     = windows.NewLazySystemDLL("advapi32.dll")
	procCredWriteW  = modadvapi32.NewProc("CredWriteW")
	procCredDeleteW = modadvapi32.NewProc("CredDeleteW")
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
)

// credential mirrors CREDENTIALW. Go's field alignment matches the C struct
// on both 386 and amd64.
type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// storeRDPCredential saves a password where mstsc looks for it, which is the
// same place its "Remember me" tick box writes. Takes the full target name so
// tests can use one that mstsc will never read.
//
// The blob is UTF-16LE with no terminator, the same framing the .rdp file's
// DPAPI password uses.
func storeRDPCredential(target, user, password string) error {
	targetPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	userPtr, err := windows.UTF16PtrFromString(user)
	if err != nil {
		return err
	}
	blob := utf16LE(password)

	cred := credential{
		Type:       credTypeGeneric,
		TargetName: targetPtr,
		UserName:   userPtr,
		Persist:    credPersistLocalMachine,
	}
	if len(blob) > 0 {
		cred.CredentialBlobSize = uint32(len(blob))
		cred.CredentialBlob = &blob[0]
	}

	r, _, e := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	// Everything the struct points at has to outlive the call.
	runtime.KeepAlive(targetPtr)
	runtime.KeepAlive(userPtr)
	runtime.KeepAlive(blob)
	if r == 0 {
		return fmt.Errorf("CredWrite %s: %w", target, e)
	}
	return nil
}

// deleteRDPCredential removes the entry. Best effort: it is normally absent,
// and failing to tidy up is not worth failing `rm` over.
func deleteRDPCredential(target string) {
	targetPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return
	}
	procCredDeleteW.Call(uintptr(unsafe.Pointer(targetPtr)), credTypeGeneric, 0)
	runtime.KeepAlive(targetPtr)
}

// trustRDPCert records the guest's certificate as trusted so mstsc never shows
// the self-signed-certificate warning. It speaks just enough of the RDP
// handshake to get the server into TLS, then reads the certificate it presents.
func trustRDPCert(host string, port int) error {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		return err
	}

	if _, err := conn.Write(x224ConnectionRequest()); err != nil {
		return err
	}
	confirm := make([]byte, rdpNegotiationLen)
	if _, err := io.ReadFull(conn, confirm); err != nil {
		return err
	}
	if !validX224Confirm(confirm) {
		return fmt.Errorf("unexpected X.224 reply %x", confirm)
	}

	// InsecureSkipVerify is the point of the exercise: the certificate is being
	// read so it can be pinned, not used to authenticate anything.
	tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: host})
	if err := tlsConn.Handshake(); err != nil {
		return err
	}
	certs := tlsConn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return fmt.Errorf("%s presented no certificate", addr)
	}
	// SHA-256, not the SHA-1 the internet remembers: the 2026 client writes a
	// 32-byte CertHash and ignores 20-byte legacy values (verified by diffing
	// what mstsc records when "don't ask me again" is ticked).
	thumbprint := sha256.Sum256(certs[0].Raw)
	return writeCertHash(host, thumbprint[:])
}

// writeCertHash records a certificate thumbprint the way mstsc does when the
// user ticks "don't ask me again". The key is by hostname with no port, so
// every env on 127.0.0.1 shares one entry. Observed behaviour, not documented
// API.
func writeCertHash(host string, thumbprint []byte) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER,
		`Software\Microsoft\Terminal Server Client\Servers\`+host,
		registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetBinaryValue("CertHash", thumbprint)
}
