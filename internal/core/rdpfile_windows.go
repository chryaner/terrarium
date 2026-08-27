//go:build windows

package core

import (
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// protectPassword encrypts a password exactly the way mstsc does for
// "Remember me": DPAPI with no description and no extra entropy, which ties
// the blob to this user on this machine. mstsc decrypts `password 51:b:`
// blobs made this way, and nothing else can read it - strictly better than
// the plaintext already sitting in terrarium's state file.
//
// The plaintext must be UTF-16LE with no BOM and no NUL terminator; a
// terminator decrypts to a password with a stray NUL on the end.
func protectPassword(pw string) (string, error) {
	if pw == "" {
		return "", fmt.Errorf("no password to protect")
	}
	in := utf16LE(pw)
	inBlob := windows.DataBlob{Size: uint32(len(in)), Data: &in[0]}

	var out windows.DataBlob
	err := windows.CryptProtectData(&inBlob, nil, nil, 0, nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	if err != nil {
		return "", fmt.Errorf("CryptProtectData: %w", err)
	}
	// The blob is LocalAlloc'd by the API, so copy it out before freeing.
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))

	blob := make([]byte, out.Size)
	copy(blob, unsafe.Slice(out.Data, out.Size))
	return strings.ToUpper(hex.EncodeToString(blob)), nil
}

func utf16LE(s string) []byte {
	units := utf16.Encode([]rune(s))
	b := make([]byte, len(units)*2)
	for i, u := range units {
		b[i*2] = byte(u)
		b[i*2+1] = byte(u >> 8)
	}
	return b
}
