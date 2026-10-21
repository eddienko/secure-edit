package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eddienko/secure-edit/internal/vault"
)

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
	if _, err := os.Stat(vault.BackupPath(real)); err != nil {
		t.Fatal("backup missing next to target")
	}
	if _, err := os.Lstat(vault.BackupPath(link)); err == nil {
		t.Fatal("backup created next to the link")
	}
	if _, err := os.Stat(vault.LockPath(link)); err == nil {
		t.Fatal("lock created for the link name")
	}
}
