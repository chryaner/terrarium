package core

import (
	"errors"
	"strings"
)

// rdpHost is what every env's RDP forward listens on.
const rdpHost = "127.0.0.1"

// rdpNegotiationLen is the size of both the X.224 connection request we send
// and the connection confirm the server answers with.
const rdpNegotiationLen = 19

// termsrvTarget is the Credential Manager key mstsc looks up for a host - the
// same entry its "Remember me" tick box writes.
func termsrvTarget(host string) string { return "TERMSRV/" + host }

// x224ConnectionRequest opens an RDP handshake and asks the server to switch
// to TLS, which is what makes it present its certificate. Verified against a
// live guest, which answers with a 19-byte connection confirm.
//
//	03 00 00 13                TPKT: version 3, 19 bytes total
//	0e e0 00 00 00 00 00       X.224 connection request, class 0
//	01 00 08 00 03 00 00 00    RDP_NEG_REQ: PROTOCOL_SSL | PROTOCOL_HYBRID
func x224ConnectionRequest() []byte {
	return []byte{
		0x03, 0x00, 0x00, 0x13,
		0x0e, 0xe0, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x08, 0x00, 0x03, 0x00, 0x00, 0x00,
	}
}

// validX224Confirm reports whether a reply looks like the connection confirm.
// Only the TPKT version is checked: which protocol the server picked does not
// matter here, since all we want is the certificate the TLS handshake carries.
func validX224Confirm(b []byte) bool {
	return len(b) == rdpNegotiationLen && b[0] == 0x03
}

// PrepareRDPLogin makes mstsc connect without asking anything: the password
// goes into Credential Manager where mstsc looks it up, and the guest's
// self-signed certificate is pre-trusted.
//
// Neither step is needed for the connection itself, so a failure is reported
// rather than returned as fatal - the caller prints it as a note. A missing
// credential costs a login prompt, a missing certificate a one-time warning.
func (e *Engine) PrepareRDPLogin(envName string, hostPort int) error {
	_, user, password, _, err := e.SSHTarget(envName)
	if err != nil {
		return err
	}

	var degraded []string
	switch {
	case password == "":
		degraded = append(degraded, "no password recorded for this golden, so mstsc will ask for one")
	default:
		if err := storeRDPCredential(termsrvTarget(rdpHost), user, password); err != nil {
			degraded = append(degraded, "could not save the credential ("+err.Error()+"), so mstsc will ask for it")
		}
	}
	if err := trustRDPCert(rdpHost, hostPort); err != nil {
		degraded = append(degraded, "could not pre-trust the guest certificate ("+err.Error()+"), so mstsc will warn once")
	}

	if len(degraded) > 0 {
		return errors.New(strings.Join(degraded, "; "))
	}
	return nil
}

// otherEnvHasRDP reports whether any env besides the named one has had RDP
// turned on. The TERMSRV credential is keyed by host and every env forwards
// from 127.0.0.1, so they all share one entry and the last `terrarium rdp`
// wins; it is only safe to clear when the last RDP env goes away.
func (e *Engine) otherEnvHasRDP(excluding string) bool {
	for name, env := range e.St.Envs {
		if name != excluding && env.RDPPort != 0 {
			return true
		}
	}
	return false
}
