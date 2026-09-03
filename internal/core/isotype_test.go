package core

import (
	"strings"
	"testing"
)

func TestResolveISOOSType(t *testing.T) {
	cases := []struct {
		name       string
		fromRecipe string
		detected   string
		want       string
		wantSaid   string
	}{
		{
			// The reported bug: a recipe pinned to 32-bit, an x64 disc, and
			// nothing said so until Windows setup failed.
			name:       "detection overrides a wrong arch",
			fromRecipe: "Windows10", detected: "Windows10_64",
			want:     "Windows10_64",
			wantSaid: "recipe says Windows10 (x86) but the ISO is x64: using Windows10_64",
		},
		{
			// Same architecture: the recipe may name a more specific edition.
			name:       "recipe kept when the arch agrees",
			fromRecipe: "WindowsXP", detected: "WindowsXP",
			want: "WindowsXP",
		},
		{
			name:       "recipe kept when detection says nothing",
			fromRecipe: "Windows10_64", detected: "",
			want: "Windows10_64",
		},
		{
			name:       "detection fills a silent recipe",
			fromRecipe: "", detected: "Windows11_64",
			want:     "Windows11_64",
			wantSaid: "the ISO installs Windows11_64 (x64)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var said []string
			got, err := resolveISOOSType("win10", c.fromRecipe, c.detected, func(m string) { said = append(said, m) })
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
			if c.wantSaid == "" {
				return
			}
			if !strings.Contains(strings.Join(said, "\n"), c.wantSaid) {
				t.Errorf("progress should say %q, said %v", c.wantSaid, said)
			}
		})
	}
}

// Neither source knowing is the one case worth failing on, and the message has
// to name the fix: the alternative is a forty-minute install of the wrong
// thing.
func TestResolveISOOSTypeNeedsSomething(t *testing.T) {
	_, err := resolveISOOSType("win10", "", "", func(string) {})
	if err == nil {
		t.Fatal("no recipe type and no detection should fail")
	}
	for _, want := range []string{"ostype:", "recipes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}
