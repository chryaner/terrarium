package cli

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestParseDisplay(t *testing.T) {
	cases := []struct {
		name           string
		full, multimon bool
		size           string
		mstsc, freerdp []string
		wantErr        bool
	}{
		{"default window", false, false, "", nil, []string{"/dynamic-resolution"}, false},
		{"fullscreen", true, false, "", []string{"/f"}, []string{"/f"}, false},
		{"every monitor", false, true, "", []string{"/multimon"}, []string{"/multimon", "/f"}, false},
		{"fixed size", false, false, "1920x1080", []string{"/w:1920", "/h:1080"}, []string{"/size:1920x1080"}, false},
		{"size without x", false, false, "1920", nil, nil, true},
		{"size not numbers", false, false, "widexhigh", nil, nil, true},
		{"size half number", false, false, "1920x", nil, nil, true},
	}
	for _, c := range cases {
		d, err := parseDisplay(c.full, c.multimon, c.size)
		if c.wantErr {
			if err == nil || !strings.Contains(err.Error(), "WIDTHxHEIGHT") {
				t.Errorf("%s: want a WIDTHxHEIGHT error, got %v", c.name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := d.mstscArgs(); !slices.Equal(got, c.mstsc) {
			t.Errorf("%s: mstsc %v, want %v", c.name, got, c.mstsc)
		}
		if got := d.freerdpArgs(); !slices.Equal(got, c.freerdp) {
			t.Errorf("%s: freerdp %v, want %v", c.name, got, c.freerdp)
		}
	}
}

func TestFreeRDPCommandArgs(t *testing.T) {
	got := freerdpCommandArgs("127.0.0.1:42210", "terrarium", true, rdpDisplay{})
	want := []string{"/v:127.0.0.1:42210", "/cert:ignore", "/u:terrarium", "/from-stdin:force", "/dynamic-resolution"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// No password to feed means no stdin read: FreeRDP prompts on its own.
	noPw := freerdpCommandArgs("127.0.0.1:42210", "terrarium", false, rdpDisplay{full: true})
	if slices.Contains(noPw, "/from-stdin:force") || !slices.Contains(noPw, "/f") {
		t.Errorf("no-password args: %v", noPw)
	}
	for _, a := range append(got, noPw...) {
		if strings.HasPrefix(a, "/p:") {
			t.Errorf("password must never be an argument: %v", a)
		}
	}
}

func TestFindFreeRDPPrefersVersion3(t *testing.T) {
	have := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			if slices.Contains(names, n) {
				return "/usr/bin/" + n, nil
			}
			return "", errors.New("not found")
		}
	}
	cases := []struct {
		installed []string
		want      string
	}{
		{[]string{"xfreerdp", "xfreerdp3"}, "/usr/bin/xfreerdp3"},
		{[]string{"xfreerdp"}, "/usr/bin/xfreerdp"},
		{[]string{"sdl-freerdp3"}, "/usr/bin/sdl-freerdp3"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := findFreeRDP(have(c.installed...)); got != c.want {
			t.Errorf("installed %v: got %q, want %q", c.installed, got, c.want)
		}
	}
}

func TestHasDisplay(t *testing.T) {
	env := func(kv map[string]string) func(string) string {
		return func(k string) string { return kv[k] }
	}
	if hasDisplay(env(nil)) {
		t.Error("no DISPLAY and no WAYLAND_DISPLAY is a headless session")
	}
	if !hasDisplay(env(map[string]string{"DISPLAY": ":0"})) || !hasDisplay(env(map[string]string{"WAYLAND_DISPLAY": "wayland-0"})) {
		t.Error("either variable means there is somewhere to draw")
	}
}
