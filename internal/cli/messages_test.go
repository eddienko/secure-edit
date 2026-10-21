package cli

import (
	"github.com/eddienko/secure-edit/internal/keystore"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr runs f and returns what it wrote to os.Stderr.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	tmp, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = tmp
	defer func() { os.Stderr = old; tmp.Close() }()
	f()
	b, _ := os.ReadFile(tmp.Name())
	return string(b)
}

func TestMessages(t *testing.T) {
	fs := useFakeStore(t)
	appendEditor(t, "one")
	path := filepath.Join(t.TempDir(), "secrets")

	out := captureStderr(t, func() {
		withStdin(t, "pw\n")
		if err := edit(path, true, choiceAsk); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"created " + path + " (encrypted)", "password remembered in Keychain"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}

	appendEditor(t, "two")
	out = captureStderr(t, func() {
		withStdin(t, "")
		if err := edit(path, false, choiceAsk); err != nil {
			t.Fatal(err)
		}
	})
	if want := "saved " + path + " (encrypted; password from Keychain)"; !strings.Contains(out, want) {
		t.Errorf("missing %q in %q", want, out)
	}

	// no changes
	script := filepath.Join(t.TempDir(), "noop.sh")
	os.WriteFile(script, []byte("#!/bin/sh\ntrue\n"), 0o755)
	t.Setenv("EDITOR", script)
	out = captureStderr(t, func() {
		withStdin(t, "")
		if err := edit(path, false, choiceAsk); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "no changes to "+path) {
		t.Errorf("got %q", out)
	}

	// default password
	delete(fs.m, mustAccount(t, path))
	fs.m[keystore.DefaultAccount] = []byte("dpw")
	d := filepath.Join(t.TempDir(), "d")
	appendEditor(t, "one")
	withStdin(t, "")
	if err := edit(d, false, choiceDefault); err != nil {
		t.Fatal(err)
	}
	appendEditor(t, "two")
	out = captureStderr(t, func() {
		withStdin(t, "")
		if err := edit(d, false, choiceAsk); err != nil {
			t.Fatal(err)
		}
	})
	if want := "(encrypted; default password from Keychain)"; !strings.Contains(out, want) {
		t.Errorf("missing %q in %q", want, out)
	}

	// passwd and encrypt
	out = captureStderr(t, func() {
		withStdin(t, "newpw\n")
		if err := passwd(d, choiceCustom); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "password changed for "+d) {
		t.Errorf("got %q", out)
	}
	plain := filepath.Join(t.TempDir(), "plain.txt")
	os.WriteFile(plain, []byte("hello"), 0o600)
	out = captureStderr(t, func() {
		withStdin(t, "pw\n")
		if err := encrypt(plain, true, choiceCustom); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "encrypted "+plain) {
		t.Errorf("got %q", out)
	}
}

func mustAccount(t *testing.T, path string) string {
	t.Helper()
	a, err := keystore.Account(path)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
