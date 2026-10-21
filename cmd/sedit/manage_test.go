package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eddienko/secure-edit/internal/keystore"
	"github.com/eddienko/secure-edit/internal/vault"
)

// rememberedFile makes a sedit file whose password is remembered ("--remember").
func rememberedFile(t *testing.T, text string) (path string, fs *fakeStore) {
	t.Helper()
	fs = useFakeStore(t)
	appendEditor(t, text)
	path = filepath.Join(t.TempDir(), "secrets.txt")
	withStdin(t, "pw\n")
	if err := edit(path, true, choiceCustom); err != nil {
		t.Fatal(err)
	}
	return path, fs
}

func TestMvMovesTheFileItsBackupAndItsPassword(t *testing.T) {
	path, fs := rememberedFile(t, "one")
	appendEditor(t, "two") // a second save, so there is a .bak
	withStdin(t, "")
	if err := edit(path, false, choiceAsk); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	newPath := filepath.Join(dir, "renamed.txt")

	var err error
	out := captureStderr(t, func() { err = run([]string{"--mv", path, newPath}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"moved " + path + " to " + newPath, "its backup and its stored password"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	notExist(t, path)
	notExist(t, vault.BackupPath(path))
	if got := decryptFile(t, newPath, "pw"); got != "one\ntwo\n" {
		t.Errorf("got %q", got)
	}
	if got := decryptFile(t, vault.BackupPath(newPath), "pw"); got != "one\n" {
		t.Errorf("backup %q", got)
	}
	// The remembered password follows the file: it opens with no typing.
	if len(fs.m) != 1 {
		t.Errorf("entries: %v", fs.m)
	}
	withStdin(t, "")
	appendEditor(t, "three")
	if err := edit(newPath, false, choiceAsk); err != nil {
		t.Fatalf("the moved file asked for a password: %v", err)
	}
}

func TestMvIntoADirectory(t *testing.T) {
	path, _ := rememberedFile(t, "x")
	sub := filepath.Join(filepath.Dir(path), "archive")
	os.Mkdir(sub, 0o700)
	if err := run([]string{"--mv", path, sub}); err != nil {
		t.Fatal(err)
	}
	decryptFile(t, filepath.Join(sub, "secrets.txt"), "pw")
	notExist(t, path)
}

func TestMvRefusesToOverwriteOrFollowSymlinks(t *testing.T) {
	path, _ := rememberedFile(t, "x")
	dir := filepath.Dir(path)
	taken := writeFile(t, filepath.Join(dir, "taken.txt"), []byte("not yours to overwrite"))
	link := filepath.Join(dir, "link")
	os.Symlink(path, link)

	if err := run([]string{"--mv", path, taken}); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("overwrite: %v", err)
	}
	if b, _ := os.ReadFile(taken); string(b) != "not yours to overwrite" {
		t.Error("the destination was changed")
	}
	if err := run([]string{"--mv", link, filepath.Join(dir, "elsewhere")}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("symlink: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("the symlink was moved")
	}
	decryptFile(t, path, "pw")
}

func TestRmDeletesFileBackupAndPassword(t *testing.T) {
	path, fs := rememberedFile(t, "one")
	appendEditor(t, "two")
	withStdin(t, "")
	edit(path, false, choiceAsk) // creates the .bak
	fs.m[keystore.DefaultAccount] = []byte("default")

	var err error
	out := captureStderr(t, func() { err = run([]string{"--rm", "-y", path}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "removed "+path) || !strings.Contains(out, "its backup and its stored password") {
		t.Errorf("message: %q", out)
	}
	notExist(t, path)
	notExist(t, vault.BackupPath(path))
	notExist(t, vault.LockPath(path))
	if len(fs.m) != 1 || string(fs.m[keystore.DefaultAccount]) != "default" {
		t.Errorf("entries now %v: only the default should remain", fs.m)
	}
}

func TestRmSeveralFiles(t *testing.T) {
	path, _ := rememberedFile(t, "x")
	dir := filepath.Dir(path)
	other := filepath.Join(dir, "other.txt")
	appendEditor(t, "y")
	withStdin(t, "pw2\n")
	if err := edit(other, false, choiceCustom); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "keep.txt")
	withStdin(t, "pw3\n")
	edit(keep, false, choiceCustom)

	if err := run([]string{"--rm", "-y", path, other}); err != nil {
		t.Fatal(err)
	}
	notExist(t, path)
	notExist(t, other)
	decryptFile(t, keep, "pw3")
}

// One bad name must delete nothing at all.
func TestRmChecksEverythingBeforeDeleting(t *testing.T) {
	path, _ := rememberedFile(t, "x")
	plain := writeFile(t, filepath.Join(filepath.Dir(path), "plain.txt"), []byte("not encrypted"))
	err := run([]string{"--rm", "-y", path, plain, filepath.Join(filepath.Dir(path), "missing")})
	if err == nil || !strings.Contains(err.Error(), "nothing was deleted") ||
		!strings.Contains(err.Error(), "not a sedit file") || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("got %v", err)
	}
	decryptFile(t, path, "pw")
	if b, _ := os.ReadFile(plain); string(b) != "not encrypted" {
		t.Error("the plain file was touched")
	}
}

func TestRmNeedsConfirmation(t *testing.T) {
	path, _ := rememberedFile(t, "x")
	withStdin(t, "")

	// No terminal and no -y: refuse, delete nothing.
	if err := run([]string{"--rm", path}); err == nil || !strings.Contains(err.Error(), "-y") {
		t.Fatalf("got %v", err)
	}
	decryptFile(t, path, "pw")

	// At a terminal: only "y" or "yes" deletes.
	old := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	defer func() { stdinIsTerminal = old }()
	for _, answer := range []string{"n\n", "\n", "nope\n", ""} {
		withStdin(t, answer)
		out := captureStderr(t, func() {
			if err := run([]string{"--rm", path}); err == nil || !strings.Contains(err.Error(), "aborted") {
				t.Errorf("answer %q: got %v", answer, err)
			}
		})
		if !strings.Contains(out, "not a secure erase") {
			t.Errorf("the prompt doesn't warn: %q", out)
		}
		decryptFile(t, path, "pw")
	}
	withStdin(t, "yes\n")
	if err := run([]string{"--rm", path}); err != nil {
		t.Fatal(err)
	}
	notExist(t, path)
}

func TestRmRefusesSymlinksAndDirectories(t *testing.T) {
	path, _ := rememberedFile(t, "x")
	dir := filepath.Dir(path)
	link := filepath.Join(dir, "link")
	os.Symlink(path, link)
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o700)
	for _, p := range []string{link, sub} {
		if err := run([]string{"--rm", "-y", p}); err == nil {
			t.Errorf("%s: expected an error", p)
		}
	}
	decryptFile(t, path, "pw")
	if _, err := os.Lstat(link); err != nil {
		t.Error("the symlink was removed")
	}
}
