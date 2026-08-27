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
	for _, want := range []string{"enter", "ctrl-alt-del", "f12"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should list %q as valid: %v", want, err)
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
	// Bare modifiers are deliberately absent.
	for _, unwanted := range []string{"ctrl", "alt", "a", "c"} {
		if slices.Contains(names, unwanted) {
			t.Errorf("%q should not be an offered key name", unwanted)
		}
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
