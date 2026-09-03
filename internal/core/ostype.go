package core

import (
	"fmt"
	"strings"
)

// ArchOf reads the CPU architecture out of a VirtualBox guest type id. The id
// carries it as a suffix - Windows10_64, Debian_arm64, plain Windows10 for
// 32-bit - and it is the one fact about a golden that decides whether an
// installer or a binary will run in it at all. An empty type has no answer to
// give; callers print that as unknown rather than guessing x86.
func ArchOf(ostype string) string {
	switch {
	case ostype == "":
		return ""
	case strings.HasSuffix(ostype, "_arm64"):
		return "arm64"
	case strings.HasSuffix(ostype, "_64"):
		return "x64"
	default:
		return "x86"
	}
}

// osType returns the guest type recorded for a VM, probing VirtualBox and
// persisting the answer when the record has none. Records predate the field,
// and a promoted or adopted VM has no recipe to read it from, so this is the
// one place that fills it: one showvminfo the first time, then never again.
func (e *Engine) osType(vmName string, cached *string) (string, error) {
	if *cached != "" {
		return *cached, nil
	}
	ostype, err := e.VB.OSTypeID(vmName)
	if err != nil {
		return "", err
	}
	*cached = ostype
	return ostype, e.St.Save()
}

// FillOSTypes caches the guest type of every golden and env that has none, for
// the commands that list all of them. Best effort: a record whose VM has gone
// stays blank rather than failing the listing, and nothing is written when
// there was nothing to fill - `ls` must not rewrite state on every run.
func (e *Engine) FillOSTypes() error {
	changed := false
	for _, g := range e.St.Goldens {
		if g.OSType != "" {
			continue
		}
		if ostype, err := e.VB.OSTypeID(g.VMName); err == nil {
			g.OSType = ostype
			changed = true
		}
	}
	for _, env := range e.St.Envs {
		if env.OSType != "" {
			continue
		}
		// The golden's type is the same disk's type, and it is already loaded.
		if g := e.St.Goldens[env.Golden]; g != nil && g.OSType != "" {
			env.OSType = g.OSType
			changed = true
			continue
		}
		if ostype, err := e.VB.OSTypeID(env.VMName); err == nil {
			env.OSType = ostype
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return e.St.Save()
}

// OSTypeOf resolves the guest type of a golden or an env by name, filling the
// record if it was blank. Goldens win a name collision, the way `info` reads
// them.
func (e *Engine) OSTypeOf(name string) (string, error) {
	if env := e.St.Envs[name]; env != nil {
		return e.osType(env.VMName, &env.OSType)
	}
	if g := e.St.Goldens[name]; g != nil {
		return e.osType(g.VMName, &g.OSType)
	}
	return "", fmt.Errorf("no golden or env %q", name)
}
