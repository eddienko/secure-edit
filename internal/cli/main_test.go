package cli

import (
	"bytes"
	"errors"
	"github.com/eddienko/secure-edit/internal/keystore"
	"github.com/eddienko/secure-edit/internal/vault"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withStdin replaces os.Stdin with a file holding input for the test.
func withStdin(t *testing.T, input string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(input)
	f.Seek(0, 0)
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old; f.Close() })
}

func TestIsSedit(t *testing.T) {
	enc, _ := vault.Encrypt([]byte("pw"), []byte("x"), vault.DefaultKDF)
	if !vault.IsSedit(enc) || vault.IsSedit([]byte("hello")) || vault.IsSedit(nil) {
		t.Fatal("IsSedit wrong")
	}
}

func TestEncryptConvertsPlaintext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(path, []byte("my secret\n"), 0o600)
	withStdin(t, "pw\n")
	if err := encrypt(path, true, choiceAsk); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got, err := vault.Decrypt([]byte("pw"), data)
	if err != nil || string(got) != "my secret\n" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := os.Stat(vault.BackupPath(path)); err == nil {
		t.Fatal("plaintext backup was created")
	}
}

func TestEncryptRefusals(t *testing.T) {
	dir := t.TempDir()

	already := filepath.Join(dir, "already")
	enc, _ := vault.Encrypt([]byte("pw"), []byte("x"), vault.DefaultKDF)
	os.WriteFile(already, enc, 0o600)
	if err := encrypt(already, true, choiceAsk); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("want already-sedit error, got %v", err)
	}

	empty := filepath.Join(dir, "empty")
	os.WriteFile(empty, nil, 0o600)
	if err := encrypt(empty, true, choiceAsk); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want empty error, got %v", err)
	}

	// Without -y and without a terminal, it must not proceed.
	plain := filepath.Join(dir, "plain")
	os.WriteFile(plain, []byte("data"), 0o600)
	withStdin(t, "pw\n")
	if err := encrypt(plain, false, choiceAsk); err == nil {
		t.Fatal("expected refusal without -y on non-terminal")
	}
	if b, _ := os.ReadFile(plain); string(b) != "data" {
		t.Fatal("file was modified")
	}
}

func TestEditRejectsPlaintextBeforePrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.txt")
	os.WriteFile(path, []byte("not encrypted"), 0o600)
	withStdin(t, "") // would fail with EOF if a password were requested
	err := edit(path, false, choiceAsk)
	if err == nil || !strings.Contains(err.Error(), "not a sedit file") {
		t.Fatalf("got %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "not encrypted" {
		t.Fatal("file was modified")
	}
}

func TestEditTreatsEmptyFileAsNew(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "ed.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho hello >> \"$1\"\n"), 0o755)
	t.Setenv("EDITOR", script)
	t.Setenv("VISUAL", "")

	path := filepath.Join(dir, "secrets")
	os.WriteFile(path, nil, 0o600)
	withStdin(t, "pw\n")
	if err := edit(path, false, choiceAsk); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got, err := vault.Decrypt([]byte("pw"), data)
	if err != nil || string(got) != "hello\n" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := os.Stat(vault.BackupPath(path)); err == nil {
		t.Fatal("backup of empty file was created")
	}
}

type fakeStore struct{ m map[string][]byte }

func (f *fakeStore) Get(a string) ([]byte, error) {
	if pw, ok := f.m[a]; ok {
		return bytes.Clone(pw), nil
	}
	return nil, keystore.ErrNotFound
}
func (f *fakeStore) Set(a string, pw []byte) error { f.m[a] = bytes.Clone(pw); return nil }
func (f *fakeStore) Delete(a string) error {
	if _, ok := f.m[a]; !ok {
		return keystore.ErrNotFound
	}
	delete(f.m, a)
	return nil
}

func useFakeStore(t *testing.T) *fakeStore {
	t.Helper()
	fs := &fakeStore{m: map[string][]byte{}}
	old := keystore.Backend
	keystore.Backend = fs
	t.Cleanup(func() { keystore.Backend = old })
	return fs
}

// appendEditor makes $EDITOR append msg to the file.
func appendEditor(t *testing.T, msg string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "ed.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho "+msg+" >> \"$1\"\n"), 0o755)
	t.Setenv("EDITOR", script)
	t.Setenv("VISUAL", "")
}

func TestRememberThenUseStoredPassword(t *testing.T) {
	fs := useFakeStore(t)
	appendEditor(t, "one")
	path := filepath.Join(t.TempDir(), "secrets")

	withStdin(t, "pw\n")
	if err := edit(path, true, choiceAsk); err != nil {
		t.Fatal(err)
	}
	acct, _ := keystore.Account(path) // resolved only once the file exists
	if string(fs.m[acct]) != "pw" {
		t.Fatalf("password not stored: %q", fs.m[acct])
	}

	// No password on stdin: must come from the store.
	appendEditor(t, "two")
	withStdin(t, "")
	if err := edit(path, false, choiceAsk); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if got, err := vault.Decrypt([]byte("pw"), data); err != nil || string(got) != "one\ntwo\n" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestNothingStoredWithoutRemember(t *testing.T) {
	fs := useFakeStore(t)
	appendEditor(t, "one")
	path := filepath.Join(t.TempDir(), "secrets")
	withStdin(t, "pw\n")
	if err := edit(path, false, choiceAsk); err != nil {
		t.Fatal(err)
	}
	if len(fs.m) != 0 {
		t.Fatalf("store not empty: %v", fs.m)
	}
}

func TestStaleStoredPasswordIsRemoved(t *testing.T) {
	fs := useFakeStore(t)
	appendEditor(t, "one")
	path := filepath.Join(t.TempDir(), "secrets")
	withStdin(t, "pw\n")
	if err := edit(path, false, choiceAsk); err != nil {
		t.Fatal(err)
	}
	acct, _ := keystore.Account(path)
	fs.m[acct] = []byte("old-password")

	withStdin(t, "pw\n") // falls back to prompting
	appendEditor(t, "two")
	if err := edit(path, false, choiceAsk); err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.m[acct]; ok {
		t.Fatal("stale entry was not removed")
	}
}

func TestPasswdUpdatesStoredPassword(t *testing.T) {
	fs := useFakeStore(t)
	appendEditor(t, "one")
	path := filepath.Join(t.TempDir(), "secrets")
	withStdin(t, "pw\n")
	if err := edit(path, true, choiceAsk); err != nil {
		t.Fatal(err)
	}
	acct, _ := keystore.Account(path)

	withStdin(t, "newpw\n") // old password comes from the store
	if err := passwd(path, choiceAsk); err != nil {
		t.Fatal(err)
	}
	if string(fs.m[acct]) != "newpw" {
		t.Fatalf("stored password not updated: %q", fs.m[acct])
	}
	data, _ := os.ReadFile(path)
	if _, err := vault.Decrypt([]byte("newpw"), data); err != nil {
		t.Fatal(err)
	}
}

func TestForget(t *testing.T) {
	fs := useFakeStore(t)
	path := filepath.Join(t.TempDir(), "secrets")
	acct, _ := keystore.Account(path)
	fs.m[acct] = []byte("pw")
	if err := forget(path); err != nil {
		t.Fatal(err)
	}
	if err := forget(path); err == nil || !strings.Contains(err.Error(), "no stored password") {
		t.Fatalf("got %v", err)
	}
}

func TestRememberWithoutStore(t *testing.T) {
	old := keystore.Backend
	keystore.Backend = nil
	defer func() { keystore.Backend = old }()
	if err := edit("whatever", true, choiceAsk); err == nil {
		t.Fatal("expected error")
	}
	if err := forget("whatever"); err == nil {
		t.Fatal("expected error")
	}
}

func TestEditDoesNotOverwriteAFileChangedWhileEditing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets")
	if err := vault.Save(path, []byte("pw"), []byte("original\n"), nil); err != nil {
		t.Fatal(err)
	}
	// The "editor" adds a line to the text, and meanwhile something else (a sync
	// tool, say) replaces the file.
	script := filepath.Join(dir, "ed.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho mine >> \"$1\"\nprintf theirs > '"+path+"'\n"), 0o755)
	t.Setenv("EDITOR", script)
	t.Setenv("VISUAL", "")
	t.Setenv("SEDIT_EDITOR", "")

	withStdin(t, "pw\n")
	err := edit(path, false, choiceAsk)
	var ce *vault.ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("want a conflict, got %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "theirs" {
		t.Errorf("the other change was overwritten: %q", b)
	}
	data, _ := os.ReadFile(ce.Conflict)
	if got, derr := vault.Decrypt([]byte("pw"), data); derr != nil || string(got) != "original\nmine\n" {
		t.Errorf("the conflict file holds %q, %v", got, derr)
	}
	if _, err := os.Stat(vault.BackupPath(path)); err == nil {
		t.Error("a backup of the other change would be wrong: nothing was saved over it")
	}
}
