package keys

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Values checked against the PC scancode set 1 table. Getting one wrong sends
// a guest a keystroke nobody asked for, so they are spelled out here rather
// than derived the same way the code derives them.
func TestSequences(t *testing.T) {
	cases := []struct {
		names []string
		want  []string
	}{
		{[]string{"enter"}, []string{"1c", "9c"}},
		{[]string{"esc"}, []string{"01", "81"}},
		{[]string{"tab"}, []string{"0f", "8f"}},
		{[]string{"space"}, []string{"39", "b9"}},
		{[]string{"backspace"}, []string{"0e", "8e"}},
		{[]string{"f1"}, []string{"3b", "bb"}},
		{[]string{"f10"}, []string{"44", "c4"}},
		{[]string{"f11"}, []string{"57", "d7"}},
		{[]string{"f12"}, []string{"58", "d8"}},

		// Extended keys carry e0 on both make and break.
		{[]string{"up"}, []string{"e0", "48", "e0", "c8"}},
		{[]string{"down"}, []string{"e0", "50", "e0", "d0"}},
		{[]string{"left"}, []string{"e0", "4b", "e0", "cb"}},
		{[]string{"right"}, []string{"e0", "4d", "e0", "cd"}},
		{[]string{"home"}, []string{"e0", "47", "e0", "c7"}},
		{[]string{"end"}, []string{"e0", "4f", "e0", "cf"}},
		{[]string{"pgup"}, []string{"e0", "49", "e0", "c9"}},
		{[]string{"pgdn"}, []string{"e0", "51", "e0", "d1"}},
		{[]string{"delete"}, []string{"e0", "53", "e0", "d3"}},
		{[]string{"win"}, []string{"e0", "5b", "e0", "db"}},

		// Combos: modifiers down, key down, key up, modifiers up in reverse.
		{[]string{"alt-f4"}, []string{"38", "3e", "be", "b8"}},
		{[]string{"alt-tab"}, []string{"38", "0f", "8f", "b8"}},
		{[]string{"ctrl-c"}, []string{"1d", "2e", "ae", "9d"}},
		{[]string{"ctrl-v"}, []string{"1d", "2f", "af", "9d"}},
		{[]string{"ctrl-a"}, []string{"1d", "1e", "9e", "9d"}},
		{[]string{"ctrl-z"}, []string{"1d", "2c", "ac", "9d"}},
		{[]string{"ctrl-alt-del"}, []string{"1d", "38", "e0", "53", "e0", "d3", "b8", "9d"}},

		// ctrl-x is what a GRUB prompt boots on: ctrl down, x down, x up,
		// ctrl up. The rest of the alphabet is checked below.
		{[]string{"ctrl-x"}, []string{"1d", "2d", "ad", "9d"}},

		// Several names flatten into one injection.
		{[]string{"tab", "enter"}, []string{"0f", "8f", "1c", "9c"}},
	}

	for _, c := range cases {
		got, err := Sequences(c.names)
		if err != nil {
			t.Errorf("%v: %v", c.names, err)
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%v: got %v, want %v", c.names, got, c.want)
		}
	}
}

// The ctrl chords come off three row strings, so one typo in a row would move
// every letter after it. Make codes spelled out, letter by letter, from the
// set 1 table; the break code is the make code with bit 7 set.
func TestCtrlChordsCoverTheAlphabet(t *testing.T) {
	makeCode := map[string]byte{
		"a": 0x1e, "b": 0x30, "c": 0x2e, "d": 0x20, "e": 0x12,
		"f": 0x21, "g": 0x22, "h": 0x23, "i": 0x17, "j": 0x24,
		"k": 0x25, "l": 0x26, "m": 0x32, "n": 0x31, "o": 0x18,
		"p": 0x19, "q": 0x10, "r": 0x13, "s": 0x1f, "t": 0x14,
		"u": 0x16, "v": 0x2f, "w": 0x11, "x": 0x2d, "y": 0x15,
		"z": 0x2c,
	}
	if len(makeCode) != 26 {
		t.Fatalf("the alphabet has 26 letters, this table has %d", len(makeCode))
	}
	for letter, code := range makeCode {
		name := "ctrl-" + letter
		got, err := Sequences([]string{name})
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		// Modifier down, letter down, letter up, modifier up.
		want := []string{"1d", hex(code), hex(code | 0x80), "9d"}
		if !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
}

// Every key must release what it presses, or the guest is left with a stuck
// modifier and every later keystroke is wrong.
func TestEveryKeyReleases(t *testing.T) {
	for _, name := range Names() {
		seq, err := Sequences([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		if len(seq)%2 != 0 {
			t.Errorf("%s: odd sequence length %d: %v", name, len(seq), seq)
		}
		var down, up int
		for _, code := range seq {
			if code == "e0" {
				continue
			}
			b, err := strconv.ParseUint(code, 16, 8)
			if err != nil {
				t.Fatalf("%s: unparsable code %q", name, code)
			}
			if b&0x80 == 0 {
				down++
			} else {
				up++
			}
		}
		if down != up {
			t.Errorf("%s: %d presses but %d releases: %v", name, down, up, seq)
		}
	}
}

func TestUnknownKeyListsValidOnes(t *testing.T) {
	_, err := Sequences([]string{"enter", "hadouken"})
	if err == nil {
		t.Fatal("expected an error for an unknown key")
	}
	if !strings.Contains(err.Error(), "hadouken") {
		t.Errorf("error should name the offending key: %v", err)
	}
	for _, want := range []string{"enter", "ctrl-alt-del", "f12", "ctrl-x"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should list %q as valid: %v", want, err)
		}
	}
}

// Near misses must fail rather than land as something else: alt-<letter> is
// not offered, and the name Summary prints for the run is not itself a key.
func TestNearMissesStillError(t *testing.T) {
	for _, name := range []string{"alt-x", "ctrl-1", "ctrl-", "ctrl-a..ctrl-z"} {
		if _, err := Sequences([]string{name}); err == nil {
			t.Errorf("%q should not be a valid key name", name)
		}
	}
}

func TestNamesSortedAndComplete(t *testing.T) {
	names := Names()
	if !slices.IsSorted(names) {
		t.Errorf("names must be sorted: %v", names)
	}
	for _, want := range []string{
		"enter", "tab", "esc", "space", "backspace", "delete",
		"up", "down", "left", "right", "home", "end", "pgup", "pgdn",
		"f1", "f12", "win",
		"alt-tab", "alt-f4", "ctrl-alt-del", "ctrl-c", "ctrl-v", "ctrl-a", "ctrl-z",
	} {
		if !slices.Contains(names, want) {
			t.Errorf("missing key name %q", want)
		}
	}
	// The whole alphabet is chorded with ctrl, not a favoured handful.
	for _, letter := range "abcdefghijklmnopqrstuvwxyz" {
		want := "ctrl-" + string(letter)
		if !slices.Contains(names, want) {
			t.Errorf("missing key name %q", want)
		}
	}
	// Bare modifiers are deliberately absent, and so are bare letters: typing
	// them is the type command's job.
	unwanted := []string{"ctrl", "alt"}
	for _, letter := range "abcdefghijklmnopqrstuvwxyz" {
		unwanted = append(unwanted, string(letter))
	}
	for _, name := range unwanted {
		if slices.Contains(names, name) {
			t.Errorf("%q should not be an offered key name", name)
		}
	}
}

// Summary is what help text and the MCP tool description carry, so it must
// stay short without hiding anything but the run it names.
func TestSummaryCollapsesTheCtrlRun(t *testing.T) {
	sum := Summary()
	if !slices.IsSorted(sum) {
		t.Errorf("summary must be sorted: %v", sum)
	}
	if !slices.Contains(sum, "ctrl-a..ctrl-z") {
		t.Errorf("summary should name the ctrl run: %v", sum)
	}
	for _, letter := range "abcdefghijklmnopqrstuvwxyz" {
		if unwanted := "ctrl-" + string(letter); slices.Contains(sum, unwanted) {
			t.Errorf("%q should be collapsed into the run: %v", unwanted, sum)
		}
	}
	// Everything that is not a ctrl-<letter> survives intact.
	for _, want := range []string{"enter", "f10", "delete", "alt-f4", "ctrl-alt-del"} {
		if !slices.Contains(sum, want) {
			t.Errorf("summary dropped %q: %v", want, sum)
		}
	}
	if len(sum) != len(Names())-25 {
		t.Errorf("summary should trade 26 names for 1, got %d from %d", len(sum), len(Names()))
	}
}

func TestSequencesNormalisesInput(t *testing.T) {
	got, err := Sequences([]string{" ENTER "})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"1c", "9c"}) {
		t.Errorf("case and spacing should be tolerated, got %v", got)
	}
}

func TestSequencesRejectsEmpty(t *testing.T) {
	if _, err := Sequences(nil); err == nil {
		t.Error("expected an error when no keys are given")
	}
}
