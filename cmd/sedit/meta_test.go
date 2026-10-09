package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionString(t *testing.T) {
	if versionString() == "" {
		t.Fatal("empty version")
	}
	old := buildVersion
	buildVersion = "v9.9.9"
	defer func() { buildVersion = old }()
	if got := versionString(); got != "v9.9.9" {
		t.Fatalf("got %q", got)
	}
}

func TestResolvePath(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	real := filepath.Join(dir, "real")
	os.WriteFile(real, []byte("x"), 0o600)
	link := filepath.Join(dir, "link")
	os.Symlink("real", link) // relative target
	dangling := filepath.Join(dir, "dangling")
	os.Symlink("nowhere", dangling)

	for path, want := range map[string]string{
		real:                         real,
		link:                         real,
		filepath.Join(dir, "absent"): filepath.Join(dir, "absent"),
	} {
		if got, err := resolvePath(path); err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", path, got, err, want)
		}
	}
	if _, err := resolvePath(dangling); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("dangling: got %v", err)
	}
}

// Editing through a symlink must leave the link in place and act on the target.
func TestRunThroughSymlink(t *testing.T) {
	useFakeStore(t)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	real := filepath.Join(dir, "real.enc")
	link := filepath.Join(dir, "link.enc")

	appendEditor(t, "one")
	withStdin(t, "pw\n")
	if err := run([]string{real}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.enc", link); err != nil {
		t.Fatal(err)
	}

	appendEditor(t, "two")
	withStdin(t, "pw\n")
	if err := run([]string{link}); err != nil {
		t.Fatal(err)
	}

	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link was replaced: %v %v", fi, err)
	}
	if got := decryptFile(t, real, "pw"); got != "one\ntwo\n" {
		t.Fatalf("target not updated: %q", got)
	}
	if _, err := os.Stat(backupPath(real)); err != nil {
		t.Fatal("backup missing next to target")
	}
	if _, err := os.Lstat(backupPath(link)); err == nil {
		t.Fatal("backup created next to the link")
	}
	if _, err := os.Stat(lockPath(link)); err == nil {
		t.Fatal("lock created for the link name")
	}
}
