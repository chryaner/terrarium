// Package paths says where terrarium keeps its files on this host.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// BaseEnv is the environment variable that moves the data directory: the
// per-user local app data on Windows, the XDG data home elsewhere.
var BaseEnv = baseEnv(runtime.GOOS)

func baseEnv(goos string) string {
	if goos == "windows" {
		return "LOCALAPPDATA"
	}
	return "XDG_DATA_HOME"
}

// DataDir holds everything terrarium owns on the host: state, downloaded
// images, local recipes and ISOs, generated keys and seeds.
func DataDir() (string, error) {
	base := os.Getenv(BaseEnv)
	if base == "" {
		var err error
		if base, err = fallbackBase(runtime.GOOS); err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "terrarium"), nil
}

func fallbackBase(goos string) (string, error) {
	if goos == "windows" {
		return os.UserConfigDir()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

// RecipeDir is where a user drops private recipes.
func RecipeDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "recipes"), nil
}

// RecipeDirHint names the recipe directory for a message, even when it
// cannot be resolved.
func RecipeDirHint() string {
	if d, err := RecipeDir(); err == nil {
		return d
	}
	return "the terrarium recipes directory"
}

// MakePrivate creates dir owner-only, and tightens it if an older terrarium
// made it 0755: it holds state with guest passwords, SSH keys and the web
// service secret. Windows ACLs on the profile directory already do this job.
func MakePrivate(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}
