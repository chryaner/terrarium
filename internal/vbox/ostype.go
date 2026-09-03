package vbox

import (
	"regexp"
	"strings"
)

// DetectISOType asks VirtualBox what an installation ISO installs and returns
// the guest type id createvm takes (Windows10_64), so a recipe does not have
// to guess the guest architecture and be silently wrong. Empty when VirtualBox
// does not recognise the disc.
//
// The exit status is deliberately not fatal: VirtualBox returns E_NOTIMPL for
// a disc it recognises but cannot fully drive - Windows XP, for instance -
// and still prints everything it worked out. The call has only really failed
// when nothing parses out of it.
func (c *Client) DetectISOType(iso string) (string, error) {
	out, err := c.runRaw(slowTimeout, "unattended", "detect", "--iso="+iso)
	typeID := parseDetectISOType(out)
	if typeID == "" && err != nil {
		return "", err
	}
	return typeID, nil
}

// detectTypeRe matches the indented `    OS TypeId    = Windows10_64` line.
// Anything else VBoxManage prints - the header, the per-image lines of a
// multi-edition Windows ISO - does not have that shape and falls through.
var detectTypeRe = regexp.MustCompile(`(?m)^[ \t]+OS TypeId[ \t]*=[ \t]*(.*?)[ \t\r]*$`)

func parseDetectISOType(out string) string {
	m := detectTypeRe.FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	return m[1]
}

// OSTypeID is a VM's guest type as the id createvm and recipes use
// (Windows10_64). showvminfo only answers with the description, so it is
// mapped back through the catalogue; the id is the spelling that carries the
// architecture, which is the whole reason terrarium records it.
func (c *Client) OSTypeID(vm string) (string, error) {
	desc, err := c.OSType(vm)
	if err != nil {
		return "", err
	}
	ids, err := c.osTypeIDs()
	if err != nil {
		return "", err
	}
	if id, ok := ids[desc]; ok {
		return id, nil
	}
	// A description this VirtualBox does not list: report it verbatim rather
	// than nothing, and let the caller decide what it is worth.
	return desc, nil
}

// osTypeIDs maps guest-type description back to id. Read once per process and
// kept: it is a static catalogue compiled into VirtualBox, and every golden
// and env would otherwise pay for the same listing.
func (c *Client) osTypeIDs() (map[string]string, error) {
	c.osTypesOnce.Do(func() {
		out, err := c.run("list", "ostypes")
		c.osTypesErr = err
		c.osTypes = parseOSTypes(out)
	})
	return c.osTypes, c.osTypesErr
}

// osTypePairRe reads `ID / Description: Windows10_64 -- Windows 10 (64-bit)`.
var osTypePairRe = regexp.MustCompile(`^ID / Description:\s+(\S+)\s+--\s+(.*)$`)

func parseOSTypes(out string) map[string]string {
	ids := map[string]string{}
	var lastID string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := osTypePairRe.FindStringSubmatch(line); m != nil {
			ids[strings.TrimSpace(m[2])] = m[1]
			continue
		}
		// VirtualBox 7.0 and older split the pair over two lines.
		if rest, ok := strings.CutPrefix(line, "ID:"); ok {
			lastID = strings.TrimSpace(rest)
		}
		if rest, ok := strings.CutPrefix(line, "Description:"); ok && lastID != "" {
			ids[strings.TrimSpace(rest)] = lastID
			lastID = ""
		}
	}
	return ids
}
