//go:build windows

package core

import (
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// testTarget is deliberately not a TERMSRV name: nothing here may land where
// mstsc would read it.
const testTarget = "terrarium/credtest"

var (
	procCredReadW = modadvapi32.NewProc("CredReadW")
	procCredFree  = modadvapi32.NewProc("CredFree")
)

func TestStoreAndDeleteRDPCredential(t *testing.T) {
	t.Cleanup(func() { deleteRDPCredential(testTarget) })

	const user = "terrarium"
	const password = "Terrarium1!"
	if err := storeRDPCredential(testTarget, user, password); err != nil {
		t.Fatal(err)
	}

	gotUser, gotPassword, err := readCredential(testTarget)
	if err != nil {
		t.Fatal(err)
	}
	if gotUser != user {
		t.Errorf("user: got %q, want %q", gotUser, user)
	}
	if gotPassword != password {
		t.Errorf("password: got %q, want %q", gotPassword, password)
	}

	deleteRDPCredential(testTarget)
	if _, _, err := readCredential(testTarget); err == nil {
		t.Error("credential should be gone after delete")
	}
	// Deleting what is not there is not an error.
	deleteRDPCredential(testTarget)
}

func TestStoreRDPCredentialOverwrites(t *testing.T) {
	t.Cleanup(func() { deleteRDPCredential(testTarget) })

	if err := storeRDPCredential(testTarget, "first", "one"); err != nil {
		t.Fatal(err)
	}
	if err := storeRDPCredential(testTarget, "second", "two"); err != nil {
		t.Fatal(err)
	}
	user, password, err := readCredential(testTarget)
	if err != nil {
		t.Fatal(err)
	}
	if user != "second" || password != "two" {
		t.Errorf("got %q/%q, want second/two", user, password)
	}
}

func TestStoreRDPCredentialUnicodeAndLong(t *testing.T) {
	t.Cleanup(func() { deleteRDPCredential(testTarget) })

	for _, password := range []string{"pässwörd", strings.Repeat("x", 200), `quote " slash \`} {
		if err := storeRDPCredential(testTarget, "u", password); err != nil {
			t.Errorf("%q: %v", password, err)
			continue
		}
		_, got, err := readCredential(testTarget)
		if err != nil {
			t.Errorf("%q: %v", password, err)
			continue
		}
		if got != password {
			t.Errorf("round trip: got %q, want %q", got, password)
		}
	}
}

// An empty password stores a zero-length blob rather than failing: mstsc then
// prompts, which is the same degraded outcome as no entry at all.
func TestStoreRDPCredentialEmptyPassword(t *testing.T) {
	t.Cleanup(func() { deleteRDPCredential(testTarget) })

	if err := storeRDPCredential(testTarget, "u", ""); err != nil {
		t.Fatal(err)
	}
	_, got, err := readCredential(testTarget)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func readCredential(target string) (user, password string, err error) {
	targetPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return "", "", err
	}
	var cred *credential
	r, _, e := procCredReadW.Call(
		uintptr(unsafe.Pointer(targetPtr)),
		credTypeGeneric,
		0,
		uintptr(unsafe.Pointer(&cred)),
	)
	runtime.KeepAlive(targetPtr)
	if r == 0 {
		return "", "", e
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(cred)))

	if cred.UserName != nil {
		user = windows.UTF16PtrToString(cred.UserName)
	}
	if cred.CredentialBlob != nil && cred.CredentialBlobSize > 0 {
		raw := unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize)
		units := make([]uint16, len(raw)/2)
		for i := range units {
			units[i] = uint16(raw[i*2]) | uint16(raw[i*2+1])<<8
		}
		password = string(utf16.Decode(units))
	}
	return user, password, nil
}
