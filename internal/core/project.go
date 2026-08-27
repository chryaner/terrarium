package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/chryaner/terrarium/internal/recipe"
	"gopkg.in/yaml.v3"
)

// ProjectFile is the per-project config `terrarium up` looks for.
const ProjectFile = "terrarium.yaml"

// ShareName is the VirtualBox shared folder name. The guest mounts it from
// the fstab line cloud-init writes (see internal/seed), so the two must agree.
const ShareName = "work"

// GuestSharePath is where ShareName appears inside the guest.
const GuestSharePath = "/work"

type Project struct {
	Image  string `yaml:"image"`
	Name   string `yaml:"name"`
	CPUs   int    `yaml:"cpus"`
	Memory int    `yaml:"memory"`
	Folder string `yaml:"folder"`

	// Dir is the directory holding the config, and the root of relative paths.
	Dir string `yaml:"-"`
}

// FindProject walks up from startDir looking for the config, the way git
// finds its repo root.
func FindProject(startDir string) (*Project, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return nil, err
	}
	for {
		path := filepath.Join(dir, ProjectFile)
		if _, err := os.Stat(path); err == nil {
			return loadProject(path)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, fmt.Errorf("no %s in %s or any parent directory", ProjectFile, startDir)
		}
		dir = parent
	}
}

func loadProject(path string) (*Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p := &Project{Dir: filepath.Dir(path)}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	// A silently ignored `memery: 4096` is worse than a parse error.
	dec.KnownFields(true)
	// An empty file decodes to io.EOF; the missing-image check below covers it.
	if err := dec.Decode(p); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	if p.Image == "" {
		return nil, fmt.Errorf("%s: image is required (one of: %s)", path, strings.Join(recipe.Names(), ", "))
	}
	if p.Folder == "" {
		p.Folder = "."
	}
	if p.Name == "" {
		p.Name = SanitizeName(filepath.Base(p.Dir))
	}
	if !nameRe.MatchString(p.Name) {
		return nil, fmt.Errorf("%s: unusable env name %q: set `name:` to letters, digits and dashes", path, p.Name)
	}
	return p, nil
}

// HostShare resolves the folder to share with the guest, empty when disabled.
//
// A relative folder may not climb out of the project: terrarium.yaml is a
// committed file, and `folder: ../../..` in someone else's repo would mount
// the home directory of whoever checked it out. An absolute path is an
// explicit choice by the person running it, and is taken as given.
func (p *Project) HostShare() (string, error) {
	if p.Folder == "" || strings.EqualFold(p.Folder, "none") {
		return "", nil
	}
	if filepath.IsAbs(p.Folder) {
		return filepath.Clean(p.Folder), nil
	}
	abs, err := filepath.Abs(filepath.Join(p.Dir, p.Folder))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(p.Dir, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s: folder %q leaves the project directory; give an absolute path to share something outside it",
			ProjectFile, p.Folder)
	}
	return abs, nil
}

var (
	notNameChar = regexp.MustCompile(`[^a-z0-9-]+`)
	dashRun     = regexp.MustCompile(`-{2,}`)
)

// SanitizeName turns a directory name into an env name: lowercase, anything
// else folded to single dashes. The result still has to pass nameRe - a
// basename of "..." sanitizes to nothing.
func SanitizeName(s string) string {
	s = notNameChar.ReplaceAllString(strings.ToLower(s), "-")
	s = dashRun.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
