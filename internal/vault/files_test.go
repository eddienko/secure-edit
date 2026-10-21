package vault

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		if got, err := ResolvePath(path); err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", path, got, err, want)
		}
	}
	if _, err := ResolvePath(dangling); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("dangling: got %v", err)
	}
}

func TestWriteAtomicCreatesWithMode0600AndLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := WriteAtomic(path, []byte("data")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v", fi.Mode().Perm(), err)
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Errorf("directory has %d entries, want 1", len(es))
	}
}

func TestWriteAtomicKeepsExistingMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	os.WriteFile(path, []byte("old"), 0o640)
	os.Chmod(path, 0o640)
	if err := WriteAtomic(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v, want 0640", fi.Mode().Perm())
	}
	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Errorf("got %q", b)
	}
}

func TestWriteAtomicFailsWithoutDamage(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no", "such", "dir", "f")
	if err := WriteAtomic(missing, []byte("x")); err == nil {
		t.Fatal("expected an error")
	}
}

func TestSaveNewFileAndKeepBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")

	if err := Save(path, []byte("pw"), []byte("first"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(BackupPath(path)); err == nil {
		t.Error("a backup was made for a new file")
	}
	first, _ := os.ReadFile(path)
	if got, err := Decrypt([]byte("pw"), first); err != nil || string(got) != "first" {
		t.Fatalf("got %q, %v", got, err)
	}

	if err := Save(path, []byte("pw"), []byte("second"), first); err != nil {
		t.Fatal(err)
	}
	bak, err := os.ReadFile(BackupPath(path))
	if err != nil || !bytes.Equal(bak, first) {
		t.Fatalf("backup is not the previous ciphertext: %v", err)
	}
	cur, _ := os.ReadFile(path)
	if got, _ := Decrypt([]byte("pw"), cur); string(got) != "second" {
		t.Errorf("current: %q", got)
	}
}

func TestSaveFailsWhenBackupCannotBeWritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	Save(path, []byte("pw"), []byte("one"), nil)
	prev, _ := os.ReadFile(path)
	os.Mkdir(BackupPath(path), 0o700) // a directory where the backup should go
	err := Save(path, []byte("pw"), []byte("two"), prev)
	if err == nil || !strings.Contains(err.Error(), "writing backup") {
		t.Fatalf("got %v", err)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, prev) {
		t.Error("the file changed although the backup failed")
	}
}
