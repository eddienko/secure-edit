package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eddienko/secure-edit/internal/fileops"
	"github.com/eddienko/secure-edit/internal/keystore"
	"github.com/eddienko/secure-edit/internal/unlock"
	"github.com/eddienko/secure-edit/internal/vault"
)

type fakeStore struct {
	m      map[string][]byte
	setErr error
}

func (f *fakeStore) Get(a string) ([]byte, error) {
	if v, ok := f.m[a]; ok {
		return append([]byte(nil), v...), nil
	}
	return nil, keystore.ErrNotFound
}
func (f *fakeStore) Set(a string, p []byte) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.m[a] = append([]byte(nil), p...)
	return nil
}
func (f *fakeStore) Delete(a string) error {
	if _, ok := f.m[a]; !ok {
		return keystore.ErrNotFound
	}
	delete(f.m, a)
	return nil
}

// useStore replaces the real Keychain for the test. It must be called first in
// every test that opens files: nothing here may touch the user's real entries.
func useStore(t *testing.T) *fakeStore {
	t.Helper()
	f := &fakeStore{m: map[string][]byte{}}
	old := keystore.Backend
	keystore.Backend = f
	t.Cleanup(func() { keystore.Backend = old })
	return f
}

func account(t *testing.T, path string) string {
	t.Helper()
	a, err := keystore.Account(path)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// useEditor sets what the editor will do to the file it is given.
func useEditor(t *testing.T, body string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "editor.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SEDIT_EDITOR", p)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	t.Setenv("SEDIT_STATUSLINE", "0")
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decrypt(t *testing.T, path, password string) string {
	t.Helper()
	got, err := vault.Decrypt([]byte(password), readFile(t, path))
	if err != nil {
		t.Fatalf("decrypt %s with %q: %v", filepath.Base(path), password, err)
	}
	return string(got)
}

func TestOpenSessionRefusesWhatIsNotASeditFile(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	if _, err := OpenSession(plainFile(t, dir, "p.txt", "plain")); err == nil || !strings.Contains(err.Error(), "not a sedit file") {
		t.Errorf("plain: %v", err)
	}
	if _, err := OpenSession(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing: expected an error")
	}
}

func TestSessionStartsLockedAndUnlocksWithTheRightPassword(t *testing.T) {
	useStore(t)
	path := seditFile(t, t.TempDir(), "a.txt", "pw", "hello")
	s, err := OpenSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Locked() || s.Plain != nil {
		t.Fatal("a new session must be locked")
	}
	if err := s.UnlockWith([]byte("wrong")); !errors.Is(err, vault.ErrBadPassword) {
		t.Fatalf("got %v", err)
	}
	if !s.Locked() || s.Password != nil {
		t.Error("a failed unlock left something behind")
	}
	if err := s.UnlockWith([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	if s.Locked() || string(s.Plain) != "hello" || s.Src != unlock.SrcPrompt {
		t.Errorf("plain %q locked=%v src=%v", s.Plain, s.Locked(), s.Src)
	}
}

func TestSessionTryStored(t *testing.T) {
	f := useStore(t)
	path := seditFile(t, t.TempDir(), "a.txt", "pw", "hello")

	s, _ := OpenSession(path)
	if ok, err := s.TryStored(nil); ok || err != nil {
		t.Fatalf("nothing stored: ok=%v err=%v", ok, err)
	}

	f.m[keystore.DefaultAccount] = []byte("pw")
	s, _ = OpenSession(path)
	if ok, _ := s.TryStored(nil); !ok || s.Src != unlock.SrcDefault || string(s.Plain) != "hello" {
		t.Errorf("default: ok=%v src=%v", ok, s.Src)
	}

	f.m[account(t, path)] = []byte("pw")
	s, _ = OpenSession(path)
	if ok, _ := s.TryStored(nil); !ok || s.Src != unlock.SrcFile {
		t.Errorf("file entry: ok=%v src=%v", ok, s.Src)
	}

	// A stale entry is removed, and the user is told.
	f.m[account(t, path)] = []byte("stale")
	delete(f.m, keystore.DefaultAccount)
	var said []string
	s, _ = OpenSession(path)
	if ok, _ := s.TryStored(func(m string) { said = append(said, m) }); ok {
		t.Error("a stale entry unlocked the file")
	}
	if _, still := f.m[account(t, path)]; still || len(said) != 1 {
		t.Errorf("stale entry removed=%v, told=%q", !still, said)
	}
}

func TestWipeZeroesTheSecrets(t *testing.T) {
	useStore(t)
	s, _ := OpenSession(seditFile(t, t.TempDir(), "a.txt", "pw", "top secret"))
	if err := s.UnlockWith([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	pw, plain := s.Password, s.Plain // the same memory, to look at after the wipe
	s.Wipe()
	if !s.Locked() || s.Password != nil {
		t.Error("not locked after Wipe")
	}
	for _, b := range append(append([]byte{}, pw...), plain...) {
		if b != 0 {
			t.Fatal("memory was not zeroed")
		}
	}
}

func TestEditSavesAndKeepsABackup(t *testing.T) {
	useStore(t)
	path := seditFile(t, t.TempDir(), "a.txt", "pw", "original\n")
	s, _ := OpenSession(path)
	s.UnlockWith([]byte("pw"))
	useEditor(t, `echo added >> "$1"`+"\n")

	saved, err := s.Edit()
	if err != nil || !saved {
		t.Fatalf("saved=%v err=%v", saved, err)
	}
	if got := decrypt(t, path, "pw"); got != "original\nadded\n" || string(s.Plain) != got {
		t.Errorf("file %q, session %q", got, s.Plain)
	}
	if got := decrypt(t, vault.BackupPath(path), "pw"); got != "original\n" {
		t.Errorf("backup %q", got)
	}
	// The session can edit again: its copy of the ciphertext is current.
	if saved, err := s.Edit(); err != nil || !saved {
		t.Fatalf("second edit: saved=%v err=%v", saved, err)
	}
	if got := decrypt(t, path, "pw"); got != "original\nadded\nadded\n" {
		t.Errorf("after second edit %q", got)
	}
}

func TestEditWithoutChangesSavesNothing(t *testing.T) {
	useStore(t)
	path := seditFile(t, t.TempDir(), "a.txt", "pw", "same\n")
	before := readFile(t, path)
	s, _ := OpenSession(path)
	s.UnlockWith([]byte("pw"))
	useEditor(t, "true\n")
	saved, err := s.Edit()
	if err != nil || saved {
		t.Fatalf("saved=%v err=%v", saved, err)
	}
	if string(readFile(t, path)) != string(before) {
		t.Error("the file was rewritten")
	}
	if _, err := os.Stat(vault.BackupPath(path)); err == nil {
		t.Error("a backup was made for no change")
	}
}

func TestEditCreatesANewFileOnlyIfSomethingWasWritten(t *testing.T) {
	useStore(t)
	dir := t.TempDir()

	path := filepath.Join(dir, "new.txt")
	s := NewSession(path, []byte("pw"))
	useEditor(t, `echo "first line" >> "$1"`+"\n")
	if saved, err := s.Edit(); err != nil || !saved {
		t.Fatalf("saved=%v err=%v", saved, err)
	}
	if got := decrypt(t, path, "pw"); got != "first line\n" {
		t.Errorf("got %q", got)
	}
	if _, err := os.Stat(vault.BackupPath(path)); err == nil {
		t.Error("a backup was made for a new file")
	}

	// Closing the editor without typing anything creates nothing.
	empty := filepath.Join(dir, "empty.txt")
	s = NewSession(empty, []byte("pw"))
	useEditor(t, "true\n")
	if saved, err := s.Edit(); err != nil || saved {
		t.Fatalf("saved=%v err=%v", saved, err)
	}
	for _, p := range []string{empty, vault.LockPath(empty), vault.BackupPath(empty)} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s was left behind", filepath.Base(p))
		}
	}
}

func TestEditRefusesToOverwriteAFileThatAppearedMeanwhile(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")
	s := NewSession(path, []byte("pw"))
	seditFile(t, dir, "new.txt", "other", "someone got there first")
	useEditor(t, `echo mine >> "$1"`+"\n")
	if _, err := s.Edit(); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("got %v", err)
	}
	if got := decrypt(t, path, "other"); got != "someone got there first" {
		t.Errorf("their file was changed: %q", got)
	}
}

func TestEditPicksUpAChangeMadeByAnotherProcess(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	path := seditFile(t, dir, "a.txt", "pw", "v1\n")
	s, _ := OpenSession(path)
	s.UnlockWith([]byte("pw"))

	// Someone else saves a new version with the same password.
	seditFile(t, dir, "a.txt", "pw", "v2 from elsewhere\n")
	useEditor(t, `echo mine >> "$1"`+"\n")
	if saved, err := s.Edit(); err != nil || !saved {
		t.Fatalf("saved=%v err=%v", saved, err)
	}
	// Their change is the base of the edit, so it is not lost.
	if got := decrypt(t, path, "pw"); got != "v2 from elsewhere\nmine\n" {
		t.Errorf("got %q", got)
	}
}

func TestEditRefusesWhenTheFileWasReKeyedElsewhere(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	path := seditFile(t, dir, "a.txt", "pw", "v1\n")
	s, _ := OpenSession(path)
	s.UnlockWith([]byte("pw"))
	seditFile(t, dir, "a.txt", "different", "v2\n")
	useEditor(t, `echo mine >> "$1"`+"\n")
	if _, err := s.Edit(); err == nil || !strings.Contains(err.Error(), "changed by someone else") {
		t.Fatalf("got %v", err)
	}
	if got := decrypt(t, path, "different"); got != "v2\n" {
		t.Errorf("the other version was overwritten: %q", got)
	}
}

func TestEditNeedsAnUnlockedSessionAndTheLock(t *testing.T) {
	useStore(t)
	path := seditFile(t, t.TempDir(), "a.txt", "pw", "x")
	s, _ := OpenSession(path)
	if _, err := s.Edit(); err == nil {
		t.Error("a locked session was edited")
	}
	s.UnlockWith([]byte("pw"))
	release, err := vault.Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	useEditor(t, `echo x >> "$1"`+"\n")
	var locked *vault.ErrLocked
	if _, err := s.Edit(); !errors.As(err, &locked) {
		t.Errorf("got %v, want ErrLocked", err)
	}
}

func TestEditorFailureChangesNothing(t *testing.T) {
	useStore(t)
	path := seditFile(t, t.TempDir(), "a.txt", "pw", "keep\n")
	before := readFile(t, path)
	s, _ := OpenSession(path)
	s.UnlockWith([]byte("pw"))
	useEditor(t, `echo partial >> "$1"`+"\nexit 3\n")
	if saved, err := s.Edit(); err == nil || saved {
		t.Fatalf("saved=%v err=%v", saved, err)
	}
	if string(readFile(t, path)) != string(before) || string(s.Plain) != "keep\n" {
		t.Error("something changed")
	}
}

func TestRekey(t *testing.T) {
	f := useStore(t)
	path := seditFile(t, t.TempDir(), "a.txt", "pw", "hello")
	f.m[account(t, path)] = []byte("pw")
	s, _ := OpenSession(path)
	if ok, _ := s.TryStored(nil); !ok {
		t.Fatal("stored password didn't unlock")
	}

	res, err := s.Rekey([]byte("newpw"))
	if err != nil || !res.Entry {
		t.Fatalf("%v %+v", err, res)
	}
	if got := decrypt(t, path, "newpw"); got != "hello" {
		t.Errorf("got %q", got)
	}
	if string(f.m[account(t, path)]) != "newpw" || string(s.Password) != "newpw" {
		t.Errorf("stored %q, session %q", f.m[account(t, path)], s.Password)
	}
	// Still usable: edit with the new password.
	useEditor(t, `echo more >> "$1"`+"\n")
	if saved, err := s.Edit(); err != nil || !saved {
		t.Fatalf("saved=%v err=%v", saved, err)
	}
	if got := decrypt(t, path, "newpw"); got != "hellomore\n" {
		t.Errorf("got %q", got)
	}
}

func TestRekeyKeepsTheSessionUsableEvenIfTheKeychainFails(t *testing.T) {
	f := useStore(t)
	path := seditFile(t, t.TempDir(), "a.txt", "pw", "hello")
	f.m[account(t, path)] = []byte("pw")
	s, _ := OpenSession(path)
	s.TryStored(nil)
	f.setErr = errors.New("keychain is locked")

	_, err := s.Rekey([]byte("newpw"))
	var se *fileops.StoreUpdateError
	if !errors.As(err, &se) {
		t.Fatalf("got %v", err)
	}
	if decrypt(t, path, "newpw") != "hello" || string(s.Password) != "newpw" {
		t.Error("the file or the session did not take the new password")
	}
}
