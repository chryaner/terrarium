//go:build windows

package core

import (
	"encoding/hex"
	"strings"
	"testing"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Round-tripping through DPAPI proves the blob is well-formed and that the
// UTF-16LE framing has no stray BOM or terminator. Whether mstsc accepts it
// is a live test, not a unit test.
func TestProtectPasswordRoundTrip(t *testing.T) {
	for _, pw := range []string{
		"Terrarium1!",
		"a",
		"pässwörd wíth ünicode",
		strings.Repeat("long", 64),
		`quotes " and \ backslashes`,
	} {
		blob, err := protectPassword(pw)
		if err != nil {
			t.Errorf("%q: %v", pw, err)
			continue
		}
		if blob != strings.ToUpper(blob) {
			t.Errorf("%q: blob should be uppercase hex, got %q", pw, blob)
		}
		if _, err := hex.DecodeString(blob); err != nil {
			t.Errorf("%q: blob is not valid hex: %v", pw, err)
			continue
		}
		got, err := unprotect(blob)
		if err != nil {
			t.Errorf("%q: %v", pw, err)
			continue
		}
		if got != pw {
			t.Errorf("round trip: got %q, want %q", got, pw)
		}
	}
}

// Encrypting the same password twice gives different blobs: DPAPI salts. A
// fixed output would mean something was badly wrong.
func TestProtectPasswordIsSalted(t *testing.T) {
	a, err := protectPassword("Terrarium1!")
	if err != nil {
		t.Fatal(err)
	}
	b, err := protectPassword("Terrarium1!")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two encryptions of the same password produced an identical blob")
	}
}

func TestProtectPasswordRejectsEmpty(t *testing.T) {
	if _, err := protectPassword(""); err == nil {
		t.Error("expected an error for an empty password")
	}
}

func TestUTF16LEHasNoBOMOrTerminator(t *testing.T) {
	got := utf16LE("ab")
	want := []byte{'a', 0, 'b', 0}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func unprotect(hexBlob string) (string, error) {
	raw, err := hex.DecodeString(hexBlob)
	if err != nil {
		return "", err
	}
	in := windows.DataBlob{Size: uint32(len(raw)), Data: &raw[0]}

	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))

	plain := make([]byte, out.Size)
	copy(plain, unsafe.Slice(out.Data, out.Size))

	units := make([]uint16, len(plain)/2)
	for i := range units {
		units[i] = uint16(plain[i*2]) | uint16(plain[i*2+1])<<8
	}
	return string(utf16.Decode(units)), nil
}
