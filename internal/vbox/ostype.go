package vbox

import (
	"regexp"
	"strings"
)

// ISOInfo is what `VBoxManage unattended detect` worked out about an
// installation ISO. TypeID is the guest type id createvm takes
// (Windows10_64), the one field terrarium acts on; the rest is reported so a
// human can see what the disc actually is.
type ISOInfo struct {
	TypeID    string
	Version   string
	Flavor    string
	Languages string
	Hints     string
	// Unattended is whether VirtualBox can drive this installer with nobody
	// at the keyboard.
	Unattended bool
}

// DetectISO asks VirtualBox what an installation ISO installs, so a recipe
// does not have to guess the guest architecture and be silently wrong.
//
// The exit status is deliberately not fatal: VirtualBox returns E_NOTIMPL for
// a disc it recognises but cannot fully drive - Windows XP, for instance -
// and still prints everything it worked out. The call has only really failed
// when nothing parses out of it.
func (c *Client) DetectISO(iso string) (ISOInfo, error) {
	out, err := c.runRaw(slowTimeout, "unattended", "detect", "--iso="+iso)
	info := parseDetectISO(out)
	if info.TypeID == "" && err != nil {
		return ISOInfo{}, err
	}
	return info, nil
}

// detectLineRe matches the indented `    OS TypeId    = Windows10_64` lines.
// Anything else VBoxManage prints - the header, the per-image lines of a
// multi-edition Windows ISO - has no `=` and falls through.
var detectLineRe = regexp.MustCompile(`(?m)^[ \t]+([A-Za-z][^=]*?)[ \t]*=[ \t]*(.*?)[ \t\r]*$`)

func parseDetectISO(out string) ISOInfo {
	var info ISOInfo
	for _, m := range detectLineRe.FindAllStringSubmatch(out, -1) {
		switch m[1] {
		case "OS TypeId":
			info.TypeID = m[2]
		case "OS Version":
			info.Version = m[2]
		case "OS Flavor":
			info.Flavor = m[2]
		case "OS Languages":
			info.Languages = m[2]
		case "OS Hints":
			info.Hints = m[2]
		case "Unattended installation supported":
			info.Unattended = m[2] == "yes"
		}
	}
	return info
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
