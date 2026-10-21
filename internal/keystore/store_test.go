package keystore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fake struct{ m map[string][]byte }

func (f *fake) Get(a string) ([]byte, error) {
	if v, ok := f.m[a]; ok {
		return v, nil
	}
	return nil, ErrNotFound
}
func (f *fake) Set(a string, p []byte) error { f.m[a] = p; return nil }
func (f *fake) Delete(a string) error        { delete(f.m, a); return nil }

func withBackend(t *testing.T, b Store) {
	t.Helper()
	old := Backend
	Backend = b
	t.Cleanup(func() { Backend = old })
}

func TestGetDefault(t *testing.T) {
	f := &fake{m: map[string][]byte{}}
	withBackend(t, f)

	if _, err := GetDefault(); !errors.Is(err, ErrNotFound) {
		t.Errorf("no default stored: got %v, want ErrNotFound", err)
	}
	f.m[DefaultAccount] = []byte("dpw")
	if got, err := GetDefault(); err != nil || string(got) != "dpw" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestGetDefaultWithoutBackend(t *testing.T) {
	withBackend(t, nil)
	if _, err := GetDefault(); !errors.Is(err, ErrNoBackend) {
		t.Errorf("got %v, want ErrNoBackend", err)
	}
}

// File accounts are absolute paths, so they can never equal the default's.
func TestAccount(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	real := filepath.Join(dir, "real")
	os.WriteFile(real, []byte("x"), 0o600)
	link := filepath.Join(dir, "link")
	os.Symlink(real, link)

	if got, err := Account(real); err != nil || got != real {
		t.Errorf("real: %q, %v", got, err)
	}
	if got, err := Account(link); err != nil || got != real {
		t.Errorf("symlink should resolve to the target: %q, %v", got, err)
	}
	// A file that doesn't exist yet: absolute, nothing to resolve.
	missing := filepath.Join(dir, "missing")
	if got, err := Account(missing); err != nil || got != missing {
		t.Errorf("missing: %q, %v", got, err)
	}
	// Relative paths become absolute.
	cwd, _ := os.Getwd()
	if got, _ := Account("some-relative-name"); got != filepath.Join(cwd, "some-relative-name") {
		t.Errorf("relative: %q", got)
	}
	if got, _ := Account("x"); got == DefaultAccount {
		t.Error("a file account collided with the default account")
	}
}
