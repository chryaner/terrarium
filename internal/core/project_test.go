package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProject(t *testing.T, dir, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ProjectFile)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProjectDefaults(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "My Project")
	writeProject(t, dir, "image: ubuntu-24.04\n")

	p, err := FindProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Image != "ubuntu-24.04" {
		t.Errorf("image: got %q", p.Image)
	}
	if p.Name != "my-project" {
		t.Errorf("name should default to the sanitized folder name, got %q", p.Name)
	}
	if p.CPUs != 0 || p.Memory != 0 {
		t.Errorf("cpus/memory should default to 0 (inherit), got %d/%d", p.CPUs, p.Memory)
	}
	if p.Folder != "." {
		t.Errorf("folder should default to \".\", got %q", p.Folder)
	}

	share, err := p.HostShare()
	if err != nil {
		t.Fatal(err)
	}
	if share != dir {
		t.Errorf("share: got %q, want %q", share, dir)
	}
}

func TestProjectExplicitValues(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "ignored-name")
	writeProject(t, dir, "image: ubuntu-22.04\nname: api\ncpus: 4\nmemory: 4096\nfolder: src\n")

	p, err := FindProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "api" || p.CPUs != 4 || p.Memory != 4096 {
		t.Errorf("unexpected project: %+v", p)
	}
	share, err := p.HostShare()
	if err != nil {
		t.Fatal(err)
	}
	if share != filepath.Join(dir, "src") {
		t.Errorf("folder should resolve against the config dir, got %q", share)
	}
}

func TestProjectShareNone(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir, "image: ubuntu-24.04\nname: api\nfolder: none\n")

	p, err := FindProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	share, err := p.HostShare()
	if err != nil {
		t.Fatal(err)
	}
	if share != "" {
		t.Errorf("folder: none must disable the share, got %q", share)
	}
}

// terrarium.yaml is a committed file: `folder: ../../..` in someone else's
// repo would mount the home directory of whoever checked it out.
func TestProjectShareCannotEscape(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "proj")
	writeProject(t, dir, "image: ubuntu-24.04\nname: api\nfolder: ../..\n")

	p, err := FindProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.HostShare(); err == nil {
		t.Error("a folder climbing out of the project must be rejected")
	}

	// A subdirectory is fine, and so is the project itself.
	for _, folder := range []string{".", "src", "src/nested", "./src"} {
		p.Folder = folder
		if _, err := p.HostShare(); err != nil {
			t.Errorf("folder %q should be allowed: %v", folder, err)
		}
	}

	// An absolute path is an explicit choice by whoever runs it.
	p.Folder = root
	share, err := p.HostShare()
	if err != nil {
		t.Errorf("an absolute path should be allowed: %v", err)
	}
	if share != filepath.Clean(root) {
		t.Errorf("got %q, want %q", share, filepath.Clean(root))
	}
}

func TestProjectRequiresImage(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir, "name: api\n")

	if _, err := FindProject(dir); err == nil || !strings.Contains(err.Error(), "image") {
		t.Errorf("expected an error naming the missing image field, got %v", err)
	}
}

func TestProjectRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir, "image: ubuntu-24.04\nmemery: 4096\n")

	if _, err := FindProject(dir); err == nil {
		t.Error("a misspelled field must not be silently ignored")
	}
}

func TestProjectRejectsUnusableName(t *testing.T) {
	// "___" sanitizes to nothing; a bare "..." would be nicer but Windows
	// will not create it.
	dir := filepath.Join(t.TempDir(), "___")
	writeProject(t, dir, "image: ubuntu-24.04\n")

	if _, err := FindProject(dir); err == nil {
		t.Error("a folder name that sanitizes to nothing must be an error")
	}
}

func TestFindProjectWalksUp(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "work")
	writeProject(t, project, "image: ubuntu-24.04\nname: api\n")

	nested := filepath.Join(project, "src", "internal", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	p, err := FindProject(nested)
	if err != nil {
		t.Fatal(err)
	}
	if p.Dir != project {
		t.Errorf("Dir: got %q, want %q", p.Dir, project)
	}
	if p.Name != "api" {
		t.Errorf("name: got %q", p.Name)
	}
}

func TestFindProjectMissing(t *testing.T) {
	_, err := FindProject(t.TempDir())
	if err == nil {
		t.Fatal("expected an error when no config exists")
	}
	if !strings.Contains(err.Error(), ProjectFile) {
		t.Errorf("error should name %s, got %v", ProjectFile, err)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"myproject", "myproject"},
		{"My Project", "my-project"},
		{"My   Project", "my-project"},
		{"terrarium.go", "terrarium-go"},
		{"__api__", "api"},
		{"-leading-and-trailing-", "leading-and-trailing"},
		{"a--b", "a-b"},
		{"CAPS", "caps"},
		{"2fast2furious", "2fast2furious"},
		{"...", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := SanitizeName(c.in); got != c.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Whatever sanitizing produces has to be a legal env name, since Fork
// validates against the same expression.
func TestSanitizeNameMatchesNameRe(t *testing.T) {
	for _, in := range []string{"My Project", "terrarium.go", "__api__", "a--b", "CAPS"} {
		got := SanitizeName(in)
		if !nameRe.MatchString(got) {
			t.Errorf("SanitizeName(%q) = %q, which nameRe rejects", in, got)
		}
	}
}
