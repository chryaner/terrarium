package core

import (
	"fmt"
	"strings"
	"time"
)

// Auth modes a golden can be in. "none" is a normal state, not a broken one:
// an adopted appliance or an XP image is driven through the console instead.
const (
	AuthKey      = "key"
	AuthPassword = "password"
	AuthNone     = "none"
)

// Info is everything `terrarium info` reports about one golden or env. It is
// a snapshot for printing, not a record: nothing here is written back.
type Info struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	VMName   string `json:"vm_name"`
	UUID     string `json:"uuid,omitempty"`
	State    string `json:"state"`
	OSType   string `json:"ostype,omitempty"`
	Arch     string `json:"arch,omitempty"`
	CPUs     int    `json:"cpus,omitempty"`
	MemoryMB int    `json:"memory_mb,omitempty"`
	Snapshot string `json:"snapshot,omitempty"`
	Auth     string `json:"auth"`
	SSHUser  string `json:"ssh_user,omitempty"`
	// AuthHint is the command that records credentials, filled in only when
	// there are none. Built here because it needs the golden's VM name, which
	// is not the golden's own name for anything terrarium built.
	AuthHint string `json:"auth_hint,omitempty"`

	// Envs only.
	Golden  string    `json:"golden,omitempty"`
	SSHPort int       `json:"ssh_port,omitempty"`
	Expires time.Time `json:"expires,omitempty"`
}

// Info gathers what is known about a golden or an env. Envs win a name
// collision, the same way screenshot resolves one: an env is what people work
// in day to day, and one name has to mean one machine everywhere.
func (e *Engine) Info(name string) (Info, error) {
	var in Info
	var g *Golden
	image := name
	switch {
	case e.St.Envs[name] != nil:
		env := e.St.Envs[name]
		g = e.St.Goldens[env.Golden]
		image = env.Golden
		in = Info{
			Kind: "env", VMName: env.VMName, UUID: env.UUID, Snapshot: cleanSnapshot,
			Golden: env.Golden, SSHPort: env.SSHPort, Expires: env.Expires,
		}
	case e.St.Goldens[name] != nil:
		g = e.St.Goldens[name]
		in = Info{Kind: "golden", VMName: g.VMName, UUID: g.UUID, Snapshot: g.Snapshot}
	default:
		return Info{}, fmt.Errorf("no golden or env %q: `terrarium ls` lists both", name)
	}
	in.Name = name
	in.Auth, in.SSHUser = authOf(g)
	if in.Auth == AuthNone && g != nil {
		in.AuthHint = AdoptHint(g.VMName, image)
	}

	// The VM is asked last, and its failures are not fatal: a record whose VM
	// someone deleted outside terrarium is exactly what info should be able to
	// show, rather than the one thing it refuses to.
	state, err := e.VB.VMState(in.VMName)
	if err != nil {
		state = "unknown (VM not in VirtualBox)"
	}
	in.State = state
	if ostype, err := e.OSTypeOf(name); err == nil {
		in.OSType = ostype
	}
	in.Arch = ArchOf(in.OSType)
	if cpus, memMB, err := e.VB.CPUMem(in.VMName); err == nil {
		in.CPUs, in.MemoryMB = cpus, memMB
	}
	return in, nil
}

// authOf reports how terrarium logs in to a golden's guests. An env inherits
// its golden's credentials, so it has the same answer.
func authOf(g *Golden) (mode, user string) {
	switch {
	case g == nil || g.SSHUser == "":
		return AuthNone, ""
	case g.SSHKey != "":
		return AuthKey, g.SSHUser
	case g.SSHPassword != "":
		return AuthPassword, g.SSHUser
	default:
		return AuthNone, g.SSHUser
	}
}

type infoField struct{ key, value string }

// FormatInfo renders an Info as aligned `key: value` lines. Pure so the
// layout is testable, and plain text so it greps.
func FormatInfo(in Info) string {
	fields := []infoField{
		{"name", in.Name},
		{"kind", in.Kind},
		{"vm", in.VMName},
		{"uuid", in.UUID},
		{"state", in.State},
		{"ostype", or(in.OSType, "unknown")},
		{"arch", or(in.Arch, "unknown")},
		{"cpus", num(in.CPUs)},
		{"memory", memory(in.MemoryMB)},
		{"snapshot", in.Snapshot},
		{"auth", authLine(in)},
	}
	if in.Kind == "env" {
		fields = append(fields,
			infoField{"golden", in.Golden},
			infoField{"ssh port", num(in.SSHPort)},
			infoField{"ttl", ttlLine(in.Expires)},
		)
	}

	width := 0
	for _, f := range fields {
		if len(f.key) > width {
			width = len(f.key)
		}
	}
	var b strings.Builder
	for _, f := range fields {
		if f.value == "" {
			continue
		}
		fmt.Fprintf(&b, "%-*s  %s\n", width+1, f.key+":", f.value)
	}
	return b.String()
}

// AdoptHint is the command that records credentials on a golden. adopt takes
// a VirtualBox VM name, which is not the golden's own name for anything
// terrarium built, so the two are spelled out separately when they differ.
func AdoptHint(vmName, image string) string {
	const creds = " --user <user> [--password <pw> | --key <path>]"
	if vmName == "" || vmName == image {
		return "terrarium adopt " + image + creds
	}
	return "terrarium adopt " + vmName + " --name " + image + creds
}

// authLine spells out the fix when there is nothing to log in with: a credless
// golden is the case a reader is most likely looking this up to resolve.
func authLine(in Info) string {
	if in.Auth != AuthNone {
		return fmt.Sprintf("%s (user %s)", in.Auth, in.SSHUser)
	}
	if in.AuthHint == "" {
		return "none (no golden record: nothing to hold credentials)"
	}
	return fmt.Sprintf("none (console only; add with `%s`)", in.AuthHint)
}

func ttlLine(expires time.Time) string {
	if expires.IsZero() {
		return "none"
	}
	return fmt.Sprintf("expires %s", expires.Format(time.RFC3339))
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func num(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

func memory(memMB int) string {
	if memMB == 0 {
		return ""
	}
	return fmt.Sprintf("%d MB", memMB)
}
