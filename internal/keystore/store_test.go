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

// hasFake is a Store that can answer Has itself, and says so.
type hasFake struct {
	fake
	asked, read int
}

func (h *hasFake) Has(a string) (bool, error) { h.asked++; _, ok := h.m[a]; return ok, nil }
func (h *hasFake) Get(a string) ([]byte, error) {
	h.read++
	return h.fake.Get(a)
}

func TestHasUsesTheStoresOwnAnswerWithoutReadingThePassword(t *testing.T) {
	h := &hasFake{fake: fake{m: map[string][]byte{"/a": []byte("pw")}}}
	withBackend(t, h)
	if ok, err := Has("/a"); err != nil || !ok {
		t.Errorf("stored: %v, %v", ok, err)
	}
	if ok, err := Has("/b"); err != nil || ok {
		t.Errorf("not stored: %v, %v", ok, err)
	}
	if h.asked != 2 || h.read != 0 {
		t.Errorf("asked %d times, read the password %d times", h.asked, h.read)
	}
}

func TestHasFallsBackToReadingAndWipesTheCopy(t *testing.T) {
	stored := []byte("secret")
	f := &fake{m: map[string][]byte{"/a": stored}}
	withBackend(t, f)
	if ok, err := Has("/a"); err != nil || !ok {
		t.Errorf("stored: %v, %v", ok, err)
	}
	if string(stored) == "secret" {
		t.Error("the password that was read was not wiped")
	}
	if ok, err := Has("/nope"); err != nil || ok {
		t.Errorf("not stored: %v, %v", ok, err)
	}
}

func TestHasWithoutABackend(t *testing.T) {
	withBackend(t, nil)
	if _, err := Has("/a"); !errors.Is(err, ErrNoBackend) {
		t.Errorf("got %v", err)
	}
	if _, err := HasDefault(); !errors.Is(err, ErrNoBackend) {
		t.Errorf("got %v", err)
	}
}

func TestHasDefault(t *testing.T) {
	f := &fake{m: map[string][]byte{}}
	withBackend(t, f)
	if ok, _ := HasDefault(); ok {
		t.Error("no default is stored")
	}
	f.m[DefaultAccount] = []byte("x")
	if ok, err := HasDefault(); err != nil || !ok {
		t.Errorf("%v, %v", ok, err)
	}
}
