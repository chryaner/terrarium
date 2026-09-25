//go:build unix

package vbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func touchExe(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestWebServiceBinary(t *testing.T) {
	t.Setenv("PATH", "")

	t.Run("beside VBoxManage", func(t *testing.T) {
		dir := t.TempDir()
		touchExe(t, filepath.Join(dir, "VBoxManage"))
		touchExe(t, filepath.Join(dir, "vboxwebsrv"))
		got, err := webServiceBinary(filepath.Join(dir, "VBoxManage"))
		if err != nil || got != filepath.Join(dir, "vboxwebsrv") {
			t.Errorf("got %q, %v", got, err)
		}
	})

	// Distro layout: /usr/bin/VBoxManage links into /usr/lib/virtualbox,
	// where vboxwebsrv lives with no link of its own.
	t.Run("beside what VBoxManage links to", func(t *testing.T) {
		root := t.TempDir()
		lib := filepath.Join(root, "lib", "virtualbox")
		touchExe(t, filepath.Join(lib, "VBox.sh"))
		touchExe(t, filepath.Join(lib, "vboxwebsrv"))
		bin := filepath.Join(root, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(lib, "VBox.sh"), filepath.Join(bin, "VBoxManage")); err != nil {
			t.Fatal(err)
		}
		got, err := webServiceBinary(filepath.Join(bin, "VBoxManage"))
		want, _ := filepath.EvalSymlinks(filepath.Join(lib, "vboxwebsrv"))
		if err != nil || got != want {
			t.Errorf("got %q, %v; want %q", got, err, want)
		}
	})

	t.Run("nowhere", func(t *testing.T) {
		dir := t.TempDir()
		touchExe(t, filepath.Join(dir, "VBoxManage"))
		_, err := webServiceBinary(filepath.Join(dir, "VBoxManage"))
		if err == nil || !strings.Contains(err.Error(), "vboxwebsrv not found") {
			t.Errorf("want a not-found error, got %v", err)
		}
	})
}
