// Package keys turns key names into the scancodes VirtualBox injects into a
// guest's keyboard. This is how terrarium drives a guest that has no SSH: an
// old Windows, an installer, anything with only a screen.
//
// Codes are PC scancode set 1. A key's break code is its make code with bit 7
// set, and the navigation cluster and Windows keys are "extended", carrying an
// e0 prefix on both. Building the table from that rule rather than typing out
// eighty hex pairs is what keeps it right.
package keys

import (
	"fmt"
	"sort"
	"strings"
)

const extPrefix = 0xe0

// Modifier make codes. Not offered as names of their own: pressing and
// releasing a modifier with nothing in between does nothing.
const (
	ctrlCode = 0x1d
	altCode  = 0x38
)

type keyDef struct {
	name string
	code byte
	ext  bool
}

var singles = []keyDef{
	{"esc", 0x01, false},
	{"backspace", 0x0e, false},
	{"tab", 0x0f, false},
	{"enter", 0x1c, false},
	{"space", 0x39, false},

	{"f1", 0x3b, false},
	{"f2", 0x3c, false},
	{"f3", 0x3d, false},
	{"f4", 0x3e, false},
	{"f5", 0x3f, false},
	{"f6", 0x40, false},
	{"f7", 0x41, false},
	{"f8", 0x42, false},
	{"f9", 0x43, false},
	{"f10", 0x44, false},
	// F11 and F12 came later and sit outside the run above.
	{"f11", 0x57, false},
	{"f12", 0x58, false},

	{"home", 0x47, true},
	{"up", 0x48, true},
	{"pgup", 0x49, true},
	{"left", 0x4b, true},
	{"right", 0x4d, true},
	{"end", 0x4f, true},
	{"down", 0x50, true},
	{"pgdn", 0x51, true},
	{"delete", 0x53, true},
	{"win", 0x5b, true},
}

// letters exist only so the combos below can reach them.
var letters = map[string]keyDef{
	"a": {"a", 0x1e, false},
	"c": {"c", 0x2e, false},
	"v": {"v", 0x2f, false},
	"z": {"z", 0x2c, false},
}

type comboDef struct {
	name string
	mods []byte
	key  string
}

var combos = []comboDef{
	{"alt-tab", []byte{altCode}, "tab"},
	{"alt-f4", []byte{altCode}, "f4"},
	{"ctrl-alt-del", []byte{ctrlCode, altCode}, "delete"},
	{"ctrl-a", []byte{ctrlCode}, "a"},
	{"ctrl-c", []byte{ctrlCode}, "c"},
	{"ctrl-v", []byte{ctrlCode}, "v"},
	{"ctrl-z", []byte{ctrlCode}, "z"},
}

var table = buildTable()

func buildTable() map[string][]string {
	t := make(map[string][]string, len(singles)+len(combos))
	byName := map[string]keyDef{}
	for _, k := range singles {
		byName[k.name] = k
		t[k.name] = append(makeCodes(k), breakCodes(k)...)
	}
	for name, k := range letters {
		byName[name] = k
	}

	for _, c := range combos {
		k, ok := byName[c.key]
		if !ok {
			panic("keys: combo " + c.name + " references unknown key " + c.key)
		}
		var seq []string
		for _, m := range c.mods {
			seq = append(seq, hex(m))
		}
		seq = append(seq, makeCodes(k)...)
		seq = append(seq, breakCodes(k)...)
		// Modifiers are released in reverse, the way fingers leave a keyboard.
		for i := len(c.mods) - 1; i >= 0; i-- {
			seq = append(seq, hex(c.mods[i]|0x80))
		}
		t[c.name] = seq
	}
	return t
}

func makeCodes(k keyDef) []string {
	if k.ext {
		return []string{hex(extPrefix), hex(k.code)}
	}
	return []string{hex(k.code)}
}

func breakCodes(k keyDef) []string {
	if k.ext {
		return []string{hex(extPrefix), hex(k.code | 0x80)}
	}
	return []string{hex(k.code | 0x80)}
}

func hex(b byte) string { return fmt.Sprintf("%02x", b) }

// Names lists every key name, sorted, for help text and error messages.
func Names() []string {
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Sequences flattens key names into the scancodes for one VBoxManage call, so
// a chord lands as a chord rather than as separate keystrokes.
func Sequences(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("no keys given (valid: %s)", strings.Join(Names(), ", "))
	}
	var out []string
	for _, name := range names {
		seq, ok := table[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			return nil, fmt.Errorf("unknown key %q (valid: %s)", name, strings.Join(Names(), ", "))
		}
		out = append(out, seq...)
	}
	return out, nil
}
