package main

import (
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
	enc, _ := Encrypt([]byte("pw"), []byte("x"), testKDF)
	if !IsSedit(enc) || IsSedit([]byte("hello")) || IsSedit(nil) {
		t.Fatal("IsSedit wrong")
	}
}

func TestEncryptConvertsPlaintext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(path, []byte("my secret\n"), 0o600)
	withStdin(t, "pw\n")
	if err := encrypt(path, true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got, err := Decrypt([]byte("pw"), data)
	if err != nil || string(got) != "my secret\n" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := os.Stat(backupPath(path)); err == nil {
		t.Fatal("plaintext backup was created")
	}
}

func TestEncryptRefusals(t *testing.T) {
	dir := t.TempDir()

	already := filepath.Join(dir, "already")
	enc, _ := Encrypt([]byte("pw"), []byte("x"), testKDF)
	os.WriteFile(already, enc, 0o600)
	if err := encrypt(already, true); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("want already-sedit error, got %v", err)
	}

	empty := filepath.Join(dir, "empty")
	os.WriteFile(empty, nil, 0o600)
	if err := encrypt(empty, true); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want empty error, got %v", err)
	}

	// Without -y and without a terminal, it must not proceed.
	plain := filepath.Join(dir, "plain")
	os.WriteFile(plain, []byte("data"), 0o600)
	withStdin(t, "pw\n")
	if err := encrypt(plain, false); err == nil {
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
	err := edit(path)
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
	if err := edit(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got, err := Decrypt([]byte("pw"), data)
	if err != nil || string(got) != "hello\n" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := os.Stat(backupPath(path)); err == nil {
		t.Fatal("backup of empty file was created")
	}
}
