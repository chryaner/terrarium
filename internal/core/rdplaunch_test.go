package core

import (
	"testing"
	"time"
)

// The request is a fixed 19 bytes and a wrong one costs a live guest to
// diagnose, so it is asserted field by field against the RDP wire format.
func TestX224ConnectionRequest(t *testing.T) {
	req := x224ConnectionRequest()

	if len(req) != rdpNegotiationLen {
		t.Fatalf("length %d, want %d", len(req), rdpNegotiationLen)
	}
	// TPKT header: version 3, reserved 0, then the total length big-endian.
	if req[0] != 0x03 || req[1] != 0x00 {
		t.Errorf("TPKT version/reserved: % x", req[:2])
	}
	if got := int(req[2])<<8 | int(req[3]); got != rdpNegotiationLen {
		t.Errorf("TPKT length field says %d, want %d", got, rdpNegotiationLen)
	}
	// X.224: length indicator counts everything after itself.
	if got := int(req[4]); got != len(req)-5 {
		t.Errorf("X.224 LI is %d, want %d", got, len(req)-5)
	}
	if req[5] != 0xe0 {
		t.Errorf("X.224 code %#x, want 0xe0 (connection request)", req[5])
	}
	// RDP_NEG_REQ: type 1, flags 0, length 8, then requested protocols.
	if req[11] != 0x01 || req[12] != 0x00 {
		t.Errorf("RDP_NEG_REQ type/flags: % x", req[11:13])
	}
	if got := int(req[13]) | int(req[14])<<8; got != 8 {
		t.Errorf("RDP_NEG_REQ length %d, want 8", got)
	}
	// PROTOCOL_SSL | PROTOCOL_HYBRID, little-endian.
	if got := uint32(req[15]) | uint32(req[16])<<8 | uint32(req[17])<<16 | uint32(req[18])<<24; got != 0x03 {
		t.Errorf("requested protocols %#x, want 0x3", got)
	}
}

func TestX224ConnectionRequestExactBytes(t *testing.T) {
	want := []byte{
		0x03, 0x00, 0x00, 0x13,
		0x0e, 0xe0, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x08, 0x00, 0x03, 0x00, 0x00, 0x00,
	}
	got := x224ConnectionRequest()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d: got %#x, want %#x\ngot:  % x\nwant: % x", i, got[i], want[i], got, want)
		}
	}
}

// The returned slice must be a fresh copy: a caller that scribbles on it must
// not corrupt the next connection.
func TestX224ConnectionRequestIsFresh(t *testing.T) {
	a := x224ConnectionRequest()
	a[0] = 0xff
	if b := x224ConnectionRequest(); b[0] != 0x03 {
		t.Error("the request buffer is shared between calls")
	}
}

func TestValidX224Confirm(t *testing.T) {
	good := make([]byte, rdpNegotiationLen)
	good[0] = 0x03
	if !validX224Confirm(good) {
		t.Error("a 19-byte TPKT reply should be accepted")
	}

	short := []byte{0x03, 0x00, 0x00, 0x13}
	if validX224Confirm(short) {
		t.Error("a short reply should be rejected")
	}
	long := make([]byte, rdpNegotiationLen+1)
	long[0] = 0x03
	if validX224Confirm(long) {
		t.Error("an over-long reply should be rejected")
	}
	wrongVersion := make([]byte, rdpNegotiationLen)
	wrongVersion[0] = 0x04
	if validX224Confirm(wrongVersion) {
		t.Error("a non-TPKT reply should be rejected")
	}
	if validX224Confirm(nil) {
		t.Error("nil should be rejected")
	}
}

func TestTermsrvTarget(t *testing.T) {
	// This exact string is what mstsc looks up; a typo means a silent prompt.
	if got := termsrvTarget("127.0.0.1"); got != "TERMSRV/127.0.0.1" {
		t.Errorf("got %q", got)
	}
}

// The credential is shared by host, so it may only be cleared when the last
// env that used RDP is removed.
func TestOtherEnvHasRDP(t *testing.T) {
	e := &Engine{St: &State{
		Goldens: map[string]*Golden{},
		Envs: map[string]*Env{
			"a": {VMName: "trr-a", SSHPort: 42200, RDPPort: 42210, Created: time.Now()},
			"b": {VMName: "trr-b", SSHPort: 42201, Created: time.Now()},
		},
	}}

	// Removing b leaves a, which does use RDP: keep the credential.
	if !e.otherEnvHasRDP("b") {
		t.Error("a still uses RDP, the credential must stay")
	}
	// Removing a leaves only b, which never used RDP: clear it.
	if e.otherEnvHasRDP("a") {
		t.Error("b never used RDP, the credential should be cleared")
	}
}

func TestOtherEnvHasRDPLastOneOut(t *testing.T) {
	e := &Engine{St: &State{
		Goldens: map[string]*Golden{},
		Envs: map[string]*Env{
			"a": {VMName: "trr-a", RDPPort: 42210},
			"b": {VMName: "trr-b", RDPPort: 42211},
		},
	}}
	if !e.otherEnvHasRDP("a") {
		t.Error("b still uses RDP, the credential must stay")
	}

	delete(e.St.Envs, "b")
	if e.otherEnvHasRDP("a") {
		t.Error("a is the last RDP env, the credential should be cleared")
	}
}
