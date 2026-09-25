package paths

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBaseEnvPerOS(t *testing.T) {
	for goos, want := range map[string]string{
		"windows": "LOCALAPPDATA",
		"linux":   "XDG_DATA_HOME",
		"darwin":  "XDG_DATA_HOME",
	} {
		if got := baseEnv(goos); got != want {
			t.Errorf("%s: got %s, want %s", goos, got, want)
		}
	}
}

func TestDataDirFollowsBaseEnv(t *testing.T) {
	base := t.TempDir()
	t.Setenv(BaseEnv, base)

	dir, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "terrarium"); dir != want {
		t.Errorf("got %q, want %q", dir, want)
	}
	rd, err := RecipeDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "terrarium", "recipes"); rd != want {
		t.Errorf("recipes: got %q, want %q", rd, want)
	}
	if RecipeDirHint() != rd {
		t.Errorf("hint should be the resolved path, got %q", RecipeDirHint())
	}
}

func TestLinuxFallbackIsXDGDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := fallbackBase("linux")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, filepath.Join(".local", "share")) {
		t.Errorf("got %q, want a path ending in .local/share", got)
	}
}
