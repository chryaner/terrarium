package vbox

import (
	"os"
	"testing"
)

// The winxp fixture is real output, captured from VirtualBox 7.2: the command
// exits non-zero with E_NOTIMPL and prints a usable detection anyway, which is
// exactly the case DetectISO must not throw away.
func TestParseDetectISO(t *testing.T) {
	winxp, err := os.ReadFile("testdata/detect-winxp.txt")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		out  string
		want ISOInfo
	}{
		{
			name: "captured winxp",
			out:  string(winxp),
			want: ISOInfo{TypeID: "WindowsXP", Version: "sp3", Languages: "en-US", Unattended: true},
		},
		{
			name: "64-bit windows",
			out: "VBoxManage.exe: info: Detected 'C:\\isos\\win10.iso' to be:\r\n" +
				"    OS TypeId    = Windows10_64\r\n" +
				"    OS Version   = 10.0.19041\r\n" +
				"    OS Flavor    = Professional\r\n" +
				"    OS Languages = en-US\r\n" +
				"    OS Hints     = \r\n" +
				"    Unattended installation supported = yes\r\n",
			want: ISOInfo{
				TypeID: "Windows10_64", Version: "10.0.19041", Flavor: "Professional",
				Languages: "en-US", Unattended: true,
			},
		},
		{
			// A disc VirtualBox recognises but cannot install unattended.
			name: "unattended not supported",
			out: "    OS TypeId    = Debian_64\n" +
				"    Unattended installation supported = no\n",
			want: ISOInfo{TypeID: "Debian_64"},
		},
		{
			// Image lines carry no `=` and must not be read as fields.
			name: "image lines ignored",
			out: "    OS TypeId    = Windows11_64\n" +
				"    Detected images (2):\n" +
				"    #1: \"Windows 11 Home\"\n" +
				"    #6: \"Windows 11 Pro\"\n",
			want: ISOInfo{TypeID: "Windows11_64"},
		},
		{
			name: "nothing detected",
			out:  "VBoxManage.exe: error: Cannot open the ISO\n",
			want: ISOInfo{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseDetectISO(c.out); got != c.want {
				t.Errorf("parseDetectISO:\n got %+v\nwant %+v", got, c.want)
			}
		})
	}
}

// The description is all showvminfo reports, and only the id says whether the
// guest is 32- or 64-bit, so this mapping is what makes the arch visible.
func TestParseOSTypes(t *testing.T) {
	modern := `Supported guest OS types:

ID / Description: Windows10 -- Windows 10 (32-bit)
Family:           Windows (Microsoft Windows)
Architecture:     x86

ID / Description: Windows10_64 -- Windows 10 (64-bit)
Family:           Windows (Microsoft Windows)
Architecture:     x86 (64-bit)

ID / Description: Debian_arm64 -- Debian (ARM 64-bit)
Family:           Linux (Linux)
Architecture:     ARMv8 (64-bit)
`
	ids := parseOSTypes(modern)
	for desc, want := range map[string]string{
		"Windows 10 (32-bit)": "Windows10",
		"Windows 10 (64-bit)": "Windows10_64",
		"Debian (ARM 64-bit)": "Debian_arm64",
	} {
		if got := ids[desc]; got != want {
			t.Errorf("%q maps to %q, want %q", desc, got, want)
		}
	}
	if len(ids) != 3 {
		t.Errorf("got %d ids, want 3: %v", len(ids), ids)
	}

	// VirtualBox 7.0 and older split the pair over two lines.
	legacy := "ID:          Ubuntu_64\nDescription: Ubuntu (64-bit)\nFamily ID:   Linux\n"
	if got := parseOSTypes(legacy)["Ubuntu (64-bit)"]; got != "Ubuntu_64" {
		t.Errorf("legacy listing: got %q, want Ubuntu_64", got)
	}
}

// Zero means "keep what the appliance shipped": an OVA nobody has re-sized
// must not be silently given terrarium's defaults.
func TestImportArgs(t *testing.T) {
	got := importArgs(`C:\ovas\noble.ova`, "trr-golden-noble", 0, 0)
	want := []string{"import", `C:\ovas\noble.ova`, "--vsys", "0", "--vmname", "trr-golden-noble", "--eula", "accept"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}

	sized := importArgs("a.ova", "vm", 4, 4096)
	if !hasFlag(sized, "--cpus", "4") || !hasFlag(sized, "--memory", "4096") {
		t.Errorf("explicit hardware should be passed through: %v", sized)
	}
}

func hasFlag(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
