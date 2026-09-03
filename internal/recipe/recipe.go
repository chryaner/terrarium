// Package recipe resolves image recipes: the small YAML files that say where
// an official cloud image lives and what to do with it. Recipes are data, not
// code, so a new image is a pull request with one file - and a user can add a
// private one without touching the repo at all.
package recipe

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/chryaner/terrarium/recipes"
	"gopkg.in/yaml.v3"
)

// Formats terrarium knows how to turn into a golden.
const (
	FormatOVA   = "ova"
	FormatQCOW2 = "qcow2"
	FormatISO   = "iso"
)

// defaultOSType is the guest type used when a qcow2 recipe does not name one.
// Ignored for OVAs, which carry their own, and for ISOs, where an unset type
// means "ask the ISO": guessing there installs a 32-bit guest from a 64-bit
// disc, which fails deep inside setup.
const defaultOSType = "Linux_64"

// Defaults for the install-from-ISO path.
const (
	DefaultUser              = "terrarium"
	defaultDiskGB            = 64
	defaultInstallTimeoutMin = 60
)

// defaultSetupTimeoutMin bounds one setup command of a derived recipe. Long
// enough for a package install, short enough that a hung command is reported
// the same day.
const defaultSetupTimeoutMin = 20

type Recipe struct {
	Name   string `yaml:"name"`
	Format string `yaml:"format"`
	// URL and Path are alternatives: exactly one is set. Path is for media
	// with no stable download, notably Windows ISOs.
	URL  string `yaml:"url,omitempty"`
	Path string `yaml:"path,omitempty"`
	// OSType is the VirtualBox guest type used to create a VM around a bare
	// disk image or a blank disk. Only qcow2 and iso need it.
	OSType string `yaml:"ostype,omitempty"`
	// SHA256 is optional: vendors who publish a "latest" URL cannot pin one.
	SHA256 string `yaml:"sha256,omitempty"`
	// Packages are installed in the guest by cloud-init. Nil means the
	// default set; an explicit empty list means none. Not used by iso.
	Packages []string `yaml:"packages,omitempty"`

	// iso only. Windows provisioning is password-first: there is no
	// cloud-init to inject a key with, so the installer creates the account.
	User              string `yaml:"user,omitempty"`
	Password          string `yaml:"password,omitempty"`
	Key               string `yaml:"key,omitempty"`
	ImageIndex        int    `yaml:"image_index,omitempty"`
	DiskGB            int    `yaml:"disk_gb,omitempty"`
	InstallTimeoutMin int    `yaml:"install_timeout_min,omitempty"`
	// Pointers so an explicit `efi: false` is distinguishable from silence.
	EFI *bool `yaml:"efi,omitempty"`
	TPM *bool `yaml:"tpm,omitempty"`
	// SSH false marks a guest that cannot run an SSH server (XP-era Windows):
	// the installer signals completion by shutting the machine down, and the
	// golden is recorded credless, driven through the console tools.
	SSH *bool `yaml:"ssh,omitempty"`
	// Additions false skips the guest-additions install, for guests current
	// additions no longer support.
	Additions *bool `yaml:"additions,omitempty"`

	// From makes this a derived recipe: it builds on the named image's golden
	// instead of on media. The build forks the base, runs Setup in the fork
	// over SSH, and flattens the result into a new golden - so shared state is
	// a YAML file, not a disk image. No media or installer fields apply; the
	// base already decided them.
	From  string   `yaml:"from,omitempty"`
	Setup []string `yaml:"setup,omitempty"`
	// SetupTimeoutMin bounds each setup command separately.
	SetupTimeoutMin int `yaml:"setup_timeout_min,omitempty"`

	// Local marks a recipe read from the user's own recipes directory.
	Local bool `yaml:"-"`
}

// UseEFI and UseTPM apply the per-format default: modern Windows expects
// both, and a recipe that wants neither has to say so.
func (r Recipe) UseEFI() bool { return r.EFI == nil || *r.EFI }
func (r Recipe) UseTPM() bool { return r.TPM == nil || *r.TPM }

// UseSSH and UseAdditions default on: a recipe opts out, never in.
func (r Recipe) UseSSH() bool       { return r.SSH == nil || *r.SSH }
func (r Recipe) UseAdditions() bool { return r.Additions == nil || *r.Additions }

// LocalDirName is where a user drops private recipes, under the terrarium
// data directory.
const LocalDirName = "recipes"

func localDir() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		if base, err = os.UserConfigDir(); err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "terrarium", LocalDirName), nil
}

// Load returns every known recipe, sorted by name. A local recipe overrides a
// built-in of the same name, so a user can pin their own URL or mirror for an
// image terrarium ships.
func Load() ([]Recipe, error) {
	dir, err := localDir()
	if err != nil {
		return nil, err
	}
	return loadFrom(dir)
}

func loadFrom(localDir string) ([]Recipe, error) {
	byName := map[string]Recipe{}

	entries, err := fs.ReadDir(recipes.FS, ".")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		data, err := fs.ReadFile(recipes.FS, entry.Name())
		if err != nil {
			return nil, err
		}
		r, err := parse(entry.Name(), data, false)
		if err != nil {
			return nil, err
		}
		byName[r.Name] = r
	}

	paths, err := filepath.Glob(filepath.Join(localDir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		r, err := parse(path, data, true)
		if err != nil {
			return nil, err
		}
		byName[r.Name] = r
	}

	out := make([]Recipe, 0, len(byName))
	for _, r := range byName {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func parse(path string, data []byte, local bool) (Recipe, error) {
	var r Recipe
	dec := yaml.NewDecoder(bytes.NewReader(data))
	// A silently ignored `formatt: ova` would download the image and then fail
	// somewhere confusing.
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil && !errors.Is(err, io.EOF) {
		return Recipe{}, fmt.Errorf("%s: %w", path, err)
	}

	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	switch {
	case r.Name == "":
		return Recipe{}, fmt.Errorf("%s: name is required", path)
	case r.Name != stem:
		return Recipe{}, fmt.Errorf("%s: name %q must match the file name", path, r.Name)
	}

	if r.From != "" {
		if err := checkDerived(path, r); err != nil {
			return Recipe{}, err
		}
		if r.SetupTimeoutMin == 0 {
			r.SetupTimeoutMin = defaultSetupTimeoutMin
		}
		r.Local = local
		return r, nil
	}

	switch {
	case len(r.Setup) > 0 || r.SetupTimeoutMin != 0:
		return Recipe{}, fmt.Errorf("%s: setup only applies to derived recipes: set from", path)
	case r.URL == "" && r.Path == "":
		return Recipe{}, fmt.Errorf("%s: one of url or path is required", path)
	case r.URL != "" && r.Path != "":
		return Recipe{}, fmt.Errorf("%s: url and path are alternatives, set only one", path)
	}
	switch r.Format {
	case FormatOVA, FormatQCOW2, FormatISO:
	case "":
		return Recipe{}, fmt.Errorf("%s: format is required (%s)", path, formatList)
	default:
		return Recipe{}, fmt.Errorf("%s: unknown format %q (%s)", path, r.Format, formatList)
	}
	if err := checkFormatFields(path, r); err != nil {
		return Recipe{}, err
	}
	if err := checkStrings(path, r); err != nil {
		return Recipe{}, err
	}

	if r.OSType == "" && r.Format != FormatISO {
		r.OSType = defaultOSType
	}
	if r.Format == FormatISO {
		if r.User == "" {
			r.User = DefaultUser
		}
		if r.DiskGB == 0 {
			r.DiskGB = defaultDiskGB
		}
		if r.InstallTimeoutMin == 0 {
			r.InstallTimeoutMin = defaultInstallTimeoutMin
		}
	}
	r.Local = local
	return r, nil
}

const formatList = FormatOVA + ", " + FormatQCOW2 + " or " + FormatISO

// userRe is an allowlist because this name is written into the user's
// ~/.ssh/config and into an unattended answer file. Backslash is allowed for
// DOMAIN\user; whitespace and control characters are not.
var userRe = regexp.MustCompile(`^[A-Za-z0-9._\\-]+$`)

// checkStrings rejects values that would escape the file they get pasted into.
// Package names end up in a #cloud-config document that runs as root on first
// boot, so a line break in one is a command injection.
func checkStrings(path string, r Recipe) error {
	if r.User != "" && !userRe.MatchString(r.User) {
		return fmt.Errorf("%s: user %q may contain only letters, digits, dot, underscore, dash and backslash", path, r.User)
	}
	for _, p := range r.Packages {
		if strings.ContainsAny(p, "\r\n") {
			return fmt.Errorf("%s: package %q contains a line break", path, p)
		}
	}
	return nil
}

// checkDerived validates a from/setup recipe: it names a base and commands,
// nothing else. Media and installer fields belong to the recipe that builds
// the base, and would be silently dead weight here.
func checkDerived(path string, r Recipe) error {
	switch {
	case r.From == r.Name:
		return fmt.Errorf("%s: from must name another image, not itself", path)
	case len(r.Setup) == 0:
		return fmt.Errorf("%s: derived recipes need setup: commands to run in a fork of %q", path, r.From)
	case r.URL != "" || r.Path != "":
		return fmt.Errorf("%s: a derived recipe has no media: it builds on the %q golden", path, r.From)
	case r.Format != "":
		return fmt.Errorf("%s: format does not apply to derived recipes", path)
	}
	for _, cmd := range r.Setup {
		if strings.TrimSpace(cmd) == "" {
			return fmt.Errorf("%s: setup contains an empty command", path)
		}
	}
	baseDecides := []struct {
		name string
		set  bool
	}{
		{"ostype", r.OSType != ""},
		{"sha256", r.SHA256 != ""},
		{"packages", r.Packages != nil},
		{"user", r.User != ""},
		{"password", r.Password != ""},
		{"key", r.Key != ""},
		{"image_index", r.ImageIndex != 0},
		{"disk_gb", r.DiskGB != 0},
		{"install_timeout_min", r.InstallTimeoutMin != 0},
		{"efi", r.EFI != nil},
		{"tpm", r.TPM != nil},
		{"ssh", r.SSH != nil},
		{"additions", r.Additions != nil},
	}
	for _, f := range baseDecides {
		if f.set {
			return fmt.Errorf("%s: %s does not apply to derived recipes, the base image decides it", path, f.name)
		}
	}
	return nil
}

// checkFormatFields rejects settings that belong to another format. They would
// otherwise be silently ignored, which is a bad way to learn that your disk
// size never applied.
func checkFormatFields(path string, r Recipe) error {
	isoOnly := []struct {
		name string
		set  bool
	}{
		{"user", r.User != ""},
		{"password", r.Password != ""},
		{"key", r.Key != ""},
		{"image_index", r.ImageIndex != 0},
		{"disk_gb", r.DiskGB != 0},
		{"install_timeout_min", r.InstallTimeoutMin != 0},
		{"efi", r.EFI != nil},
		{"tpm", r.TPM != nil},
		{"ssh", r.SSH != nil},
		{"additions", r.Additions != nil},
	}
	if r.Format == FormatISO {
		// The installer creates the account, so there is nothing else to
		// authenticate with.
		if r.Password == "" {
			return fmt.Errorf("%s: password is required for %s recipes", path, FormatISO)
		}
		if r.Packages != nil {
			return fmt.Errorf("%s: packages needs cloud-init and is not used by %s recipes", path, FormatISO)
		}
		return nil
	}
	for _, f := range isoOnly {
		if f.set {
			return fmt.Errorf("%s: %s only applies to %s recipes, not %s", path, f.name, FormatISO, r.Format)
		}
	}
	return nil
}

func Lookup(name string) (Recipe, error) {
	all, err := Load()
	if err != nil {
		return Recipe{}, err
	}
	for _, r := range all {
		if r.Name == name {
			return r, nil
		}
	}
	return Recipe{}, fmt.Errorf("unknown image %q (available: %s)", name, strings.Join(namesOf(all), ", "))
}

// Names is best-effort, for help text and error messages: a broken recipe file
// is reported by Lookup, where it can be acted on.
func Names() []string {
	all, err := Load()
	if err != nil {
		return nil
	}
	return namesOf(all)
}

func namesOf(all []Recipe) []string {
	names := make([]string, len(all))
	for i, r := range all {
		names[i] = r.Name
	}
	return names
}
