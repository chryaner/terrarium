package seed

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kdomanski/iso9660"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

func TestGenerateKeys(t *testing.T) {
	dir := t.TempDir()

	keyPath, isoPath, err := Generate(dir, "ubuntu-24.04", nil)
	if err != nil {
		t.Fatal(err)
	}
	if keyPath != filepath.Join(dir, "ubuntu-24.04", "id_ed25519") {
		t.Errorf("unexpected key path: %s", keyPath)
	}
	if _, err := os.Stat(isoPath); err != nil {
		t.Fatal(err)
	}

	priv, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(priv)
	if err != nil {
		t.Fatalf("private key must be usable by the ssh client: %v", err)
	}
	if signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		t.Errorf("expected an ed25519 key, got %s", signer.PublicKey().Type())
	}

	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey(pub); err != nil {
		t.Errorf("public key is not a valid authorized_keys line: %v", err)
	}
}

// The key must survive a rebuild: goldens created earlier authorize it.
func TestGenerateReusesKey(t *testing.T) {
	dir := t.TempDir()

	keyPath, _, err := Generate(dir, "ubuntu-24.04", nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err = Generate(dir, "ubuntu-24.04", nil); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("existing key was overwritten")
	}
}

func TestUserDataAuthorizesGeneratedKey(t *testing.T) {
	dir := t.TempDir()

	keyPath, isoPath, err := Generate(dir, "ubuntu-24.04", nil)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}

	files := readISO(t, isoPath)
	ud := files["user-data"]
	if !strings.Contains(ud, strings.TrimSpace(string(pub))) {
		t.Errorf("user-data does not carry the generated public key:\n%s", ud)
	}
	if !strings.Contains(ud, "NOPASSWD:ALL") {
		t.Errorf("user-data does not grant passwordless sudo:\n%s", ud)
	}
	if !strings.Contains(ud, "ssh_pwauth: false") {
		t.Errorf("user-data does not disable password auth:\n%s", ud)
	}
}

func TestUserDataSetsUpSharedFolder(t *testing.T) {
	dir := t.TempDir()

	_, isoPath, err := Generate(dir, "ubuntu-24.04", nil)
	if err != nil {
		t.Fatal(err)
	}
	ud := readISO(t, isoPath)["user-data"]

	for _, want := range []string{
		"packages:",
		"- virtualbox-guest-utils",
		"runcmd:",
		"- mkdir -p /work",
		"work /work vboxsf uid=1000,gid=1000,nofail,x-systemd.automount 0 0",
		">> /etc/fstab",
	} {
		if !strings.Contains(ud, want) {
			t.Errorf("user-data missing %q:\n%s", want, ud)
		}
	}
}

// An image whose distribution has no guest additions package installs nothing,
// but still gets the mount point and fstab line: nofail makes them harmless,
// and the helper may be installed by hand later.
func TestUserDataWithoutPackages(t *testing.T) {
	dir := t.TempDir()

	_, isoPath, err := Generate(dir, "alma-9", []string{})
	if err != nil {
		t.Fatal(err)
	}
	ud := readISO(t, isoPath)["user-data"]

	if strings.Contains(ud, "packages:") {
		t.Errorf("an empty package list must omit the packages block:\n%s", ud)
	}
	for _, want := range []string{"runcmd:", "- mkdir -p /work", ">> /etc/fstab"} {
		if !strings.Contains(ud, want) {
			t.Errorf("user-data missing %q:\n%s", want, ud)
		}
	}
}

func TestUserDataWithCustomPackages(t *testing.T) {
	dir := t.TempDir()

	_, isoPath, err := Generate(dir, "ubuntu-24.04", []string{"build-essential", "git"})
	if err != nil {
		t.Fatal(err)
	}
	ud := readISO(t, isoPath)["user-data"]

	if !strings.Contains(ud, "packages:\n  - build-essential\n  - git\n") {
		t.Errorf("custom packages not rendered:\n%s", ud)
	}
	if strings.Contains(ud, "virtualbox-guest-utils") {
		t.Errorf("an explicit list must replace the default, not extend it:\n%s", ud)
	}
}

// cloud-init silently does nothing with a user-data it cannot parse, and the
// fstab line is full of characters YAML has opinions about.
func TestUserDataIsValidYAML(t *testing.T) {
	dir := t.TempDir()

	_, isoPath, err := Generate(dir, "ubuntu-24.04", nil)
	if err != nil {
		t.Fatal(err)
	}
	ud := readISO(t, isoPath)["user-data"]

	var doc struct {
		Users []struct {
			Name string `yaml:"name"`
		} `yaml:"users"`
		Packages []string `yaml:"packages"`
		Runcmd   []string `yaml:"runcmd"`
	}
	if err := yaml.Unmarshal([]byte(ud), &doc); err != nil {
		t.Fatalf("user-data is not valid YAML: %v\n%s", err, ud)
	}
	if len(doc.Users) != 1 || doc.Users[0].Name != User {
		t.Errorf("unexpected users: %+v", doc.Users)
	}
	if len(doc.Packages) != 1 || doc.Packages[0] != "virtualbox-guest-utils" {
		t.Errorf("unexpected packages: %v", doc.Packages)
	}
	if len(doc.Runcmd) != 2 {
		t.Fatalf("expected 2 runcmd entries, got %v", doc.Runcmd)
	}
	if !strings.HasSuffix(doc.Runcmd[1], ">> /etc/fstab") {
		t.Errorf("fstab command did not survive YAML parsing: %q", doc.Runcmd[1])
	}
}

// The recipe loader rejects these too; this is the layer that cannot be
// bypassed by whatever else learns to call Generate.
func TestGenerateRejectsPackageWithLineBreak(t *testing.T) {
	dir := t.TempDir()

	_, _, err := Generate(dir, "evil", []string{"git", "curl\nruncmd:\n  - id"})
	if err == nil {
		t.Fatal("a package name with a newline must be rejected")
	}
	if !strings.Contains(err.Error(), "line break") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSeedISOLayout(t *testing.T) {
	dir := t.TempDir()

	_, isoPath, err := Generate(dir, "ubuntu-24.04", nil)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(isoPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := iso9660.OpenImage(f)
	if err != nil {
		t.Fatal(err)
	}
	label, err := img.Label()
	if err != nil {
		t.Fatal(err)
	}
	if label != "cidata" {
		t.Errorf("volume label must be cidata for NoCloud, got %q", label)
	}

	files := readISO(t, isoPath)
	if _, ok := files["user-data"]; !ok {
		t.Errorf("user-data missing, got %v", keys(files))
	}
	md, ok := files["meta-data"]
	if !ok {
		t.Fatalf("meta-data missing, got %v", keys(files))
	}
	if !strings.Contains(md, "instance-id: terrarium-ubuntu-24.04") {
		t.Errorf("unexpected instance-id:\n%s", md)
	}
	if !strings.Contains(md, "local-hostname: ubuntu-24-04") {
		t.Errorf("dots are not legal in hostnames:\n%s", md)
	}
}

func readISO(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	img, err := iso9660.OpenImage(f)
	if err != nil {
		t.Fatal(err)
	}
	root, err := img.RootDir()
	if err != nil {
		t.Fatal(err)
	}
	children, err := root.GetChildren()
	if err != nil {
		t.Fatal(err)
	}

	files := map[string]string{}
	for _, c := range children {
		data, err := io.ReadAll(c.Reader())
		if err != nil {
			t.Fatal(err)
		}
		files[c.Name()] = string(data)
	}
	return files
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
