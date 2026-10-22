package vault

import (
	"bytes"
	"errors"
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

func TestChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	cases := []struct {
		name string
		disk []byte // nil: the file doesn't exist
		prev []byte
		want bool
	}{
		{"same", []byte("abc"), []byte("abc"), false},
		{"different", []byte("abd"), []byte("abc"), true},
		{"gone", nil, []byte("abc"), true},
		{"new file still missing", nil, nil, false},
		{"new file now empty", []byte{}, nil, false},
		{"new file now there", []byte("x"), nil, true},
		{"emptied", []byte{}, []byte("abc"), true},
	}
	for _, c := range cases {
		os.Remove(path)
		if c.disk != nil {
			os.WriteFile(path, c.disk, 0o600)
		}
		got, err := Changed(path, c.prev)
		if err != nil || got != c.want {
			t.Errorf("%s: Changed = %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}

func TestSaveConflictWritesBesideTheFileAndNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes")
	os.WriteFile(path, []byte("theirs"), 0o600)

	err := SaveConflict(path, []byte("pw"), []byte("mine"), Meta{Title: "T"})
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.Conflict != path+".conflict" {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "notes.conflict") {
		t.Errorf("the message should name the conflict file: %v", err)
	}
	data, _ := os.ReadFile(ce.Conflict)
	if got, derr := Decrypt([]byte("pw"), data); derr != nil || string(got) != "mine" {
		t.Fatalf("conflict file = %q, %v", got, derr)
	}
	if m, _ := ReadMeta(data); m.Title != "T" {
		t.Errorf("the labels should be kept: %+v", m)
	}
	if b, _ := os.ReadFile(path); string(b) != "theirs" {
		t.Errorf("the file itself was touched: %q", b)
	}

	// A second conflict gets its own file rather than replacing the first.
	err = SaveConflict(path, []byte("pw"), []byte("mine too"), Meta{})
	if !errors.As(err, &ce) || ce.Conflict != path+".conflict.2" {
		t.Fatalf("second conflict: %v", err)
	}
	if first, _ := os.ReadFile(path + ".conflict"); len(first) == 0 {
		t.Error("the first conflict file is gone")
	}
}
