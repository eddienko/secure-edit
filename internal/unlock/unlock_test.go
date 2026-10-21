package unlock

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eddienko/secure-edit/internal/keystore"
	"github.com/eddienko/secure-edit/internal/vault"
)

type fakeStore struct {
	m       map[string][]byte
	getErr  error // returned by Get for every account, if set
	deleted []string
}

func (f *fakeStore) Get(a string) ([]byte, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if v, ok := f.m[a]; ok {
		return append([]byte(nil), v...), nil
	}
	return nil, keystore.ErrNotFound
}
func (f *fakeStore) Set(a string, p []byte) error { f.m[a] = append([]byte(nil), p...); return nil }
func (f *fakeStore) Delete(a string) error {
	f.deleted = append(f.deleted, a)
	delete(f.m, a)
	return nil
}

func useStore(t *testing.T) *fakeStore {
	t.Helper()
	f := &fakeStore{m: map[string][]byte{}}
	old := keystore.Backend
	keystore.Backend = f
	t.Cleanup(func() { keystore.Backend = old })
	return f
}

// A cheap cost, so the tests don't spend seconds in Argon2.
var fast = vault.NewKDFParams(1, 64, 1)

func encrypted(t *testing.T, password, text string) []byte {
	t.Helper()
	data, err := vault.Encrypt([]byte(password), []byte(text), fast)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// asker returns an Asker that gives the answers in turn, and counts the calls.
func asker(answers ...string) (Asker, *int) {
	n := 0
	return func(string) ([]byte, error) {
		if n >= len(answers) {
			return nil, errors.New("asked more often than expected")
		}
		n++
		return []byte(answers[n-1]), nil
	}, &n
}

func noAsk(t *testing.T) Asker {
	return func(string) ([]byte, error) {
		t.Error("the user was asked for a password")
		return nil, errors.New("unexpected ask")
	}
}

var path = filepath.Join("/nonexistent-dir", "secret.txt") // never touched: only used as a key

func acct(t *testing.T) string {
	t.Helper()
	a, err := keystore.Account(path)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestOpenAsksWhenNothingIsStored(t *testing.T) {
	useStore(t)
	ask, n := asker("pw")
	pw, plain, src, err := Open(path, encrypted(t, "pw", "hello"), ask, nil)
	if err != nil || string(plain) != "hello" || string(pw) != "pw" || src != SrcPrompt || *n != 1 {
		t.Fatalf("got %q %q %v %v after %d asks", pw, plain, src, err, *n)
	}
}

func TestOpenUsesTheFilesOwnEntry(t *testing.T) {
	f := useStore(t)
	f.m[acct(t)] = []byte("pw")
	f.m[keystore.DefaultAccount] = []byte("other") // the file's own entry must win
	_, plain, src, err := Open(path, encrypted(t, "pw", "hello"), noAsk(t), nil)
	if err != nil || string(plain) != "hello" || src != SrcFile {
		t.Fatalf("got %q %v %v", plain, src, err)
	}
}

func TestOpenFallsBackToTheDefault(t *testing.T) {
	f := useStore(t)
	f.m[keystore.DefaultAccount] = []byte("dpw")
	_, plain, src, err := Open(path, encrypted(t, "dpw", "hello"), noAsk(t), nil)
	if err != nil || string(plain) != "hello" || src != SrcDefault {
		t.Fatalf("got %q %v %v", plain, src, err)
	}
}

// A default that doesn't fit this file is kept, and the user is asked.
func TestOpenKeepsADefaultThatDoesNotFit(t *testing.T) {
	f := useStore(t)
	f.m[keystore.DefaultAccount] = []byte("dpw")
	ask, n := asker("custom")
	_, plain, src, err := Open(path, encrypted(t, "custom", "hello"), ask, nil)
	if err != nil || string(plain) != "hello" || src != SrcPrompt || *n != 1 {
		t.Fatalf("got %q %v %v", plain, src, err)
	}
	if _, ok := f.m[keystore.DefaultAccount]; !ok || len(f.deleted) != 0 {
		t.Error("the default was removed")
	}
}

func TestOpenRemovesAStaleFileEntryAndSaysSo(t *testing.T) {
	f := useStore(t)
	f.m[acct(t)] = []byte("old")
	var said []string
	ask, _ := asker("pw")
	_, plain, src, err := Open(path, encrypted(t, "pw", "hello"), ask, func(m string) { said = append(said, m) })
	if err != nil || string(plain) != "hello" || src != SrcPrompt {
		t.Fatalf("got %q %v %v", plain, src, err)
	}
	if _, ok := f.m[acct(t)]; ok {
		t.Error("the stale entry is still stored")
	}
	if len(said) != 1 || !strings.Contains(said[0], "no longer works") {
		t.Errorf("notifications: %q", said)
	}
}

func TestOpenReportsAnUnreadableKeychainAndCarriesOn(t *testing.T) {
	f := useStore(t)
	f.getErr = errors.New("keychain is locked")
	var said []string
	ask, _ := asker("pw")
	_, plain, _, err := Open(path, encrypted(t, "pw", "hello"), ask, func(m string) { said = append(said, m) })
	if err != nil || string(plain) != "hello" {
		t.Fatalf("got %q %v", plain, err)
	}
	if len(said) != 2 || !strings.Contains(said[0], "stored password") || !strings.Contains(said[1], "default password") {
		t.Errorf("notifications: %q", said)
	}
}

func TestOpenWorksWithoutAKeychain(t *testing.T) {
	old := keystore.Backend
	keystore.Backend = nil
	t.Cleanup(func() { keystore.Backend = old })
	ask, _ := asker("pw")
	if _, plain, _, err := Open(path, encrypted(t, "pw", "hello"), ask, nil); err != nil || string(plain) != "hello" {
		t.Fatalf("got %q %v", plain, err)
	}
}

func TestOpenWrongTypedPassword(t *testing.T) {
	useStore(t)
	ask, _ := asker("wrong")
	pw, plain, _, err := Open(path, encrypted(t, "pw", "hello"), ask, nil)
	if !errors.Is(err, vault.ErrBadPassword) || pw != nil || plain != nil {
		t.Fatalf("got %q %q %v", pw, plain, err)
	}
}

func TestOpenPropagatesAskErrors(t *testing.T) {
	useStore(t)
	want := errors.New("cancelled")
	_, _, _, err := Open(path, encrypted(t, "pw", "x"), func(string) ([]byte, error) { return nil, want }, nil)
	if !errors.Is(err, want) {
		t.Fatalf("got %v", err)
	}
}

// A damaged file isn't a wrong-password problem: say so, and don't ask or drop entries.
func TestOpenDamagedFile(t *testing.T) {
	f := useStore(t)
	f.m[acct(t)] = []byte("pw")
	_, _, _, err := Open(path, []byte("SEDIT\x00too short"), noAsk(t), nil)
	if err == nil || errors.Is(err, vault.ErrBadPassword) {
		t.Fatalf("got %v", err)
	}
	if _, ok := f.m[acct(t)]; !ok {
		t.Error("the entry was removed for a damaged file")
	}
}

func TestStoredDoesNotAsk(t *testing.T) {
	useStore(t)
	_, _, _, ok, err := Stored(path, encrypted(t, "pw", "x"), nil)
	if ok || err != nil {
		t.Fatalf("ok=%v err=%v, want ok=false and no error", ok, err)
	}
}

func TestStorePassword(t *testing.T) {
	f := useStore(t)
	if err := StorePassword(path, []byte("pw")); err != nil {
		t.Fatal(err)
	}
	if string(f.m[acct(t)]) != "pw" {
		t.Errorf("stored %q", f.m[acct(t)])
	}
	keystore.Backend = nil
	if err := StorePassword(path, []byte("pw")); !errors.Is(err, keystore.ErrNoBackend) {
		t.Errorf("got %v", err)
	}
}

func TestSourceNote(t *testing.T) {
	for s, want := range map[Source]string{
		SrcPrompt:  "",
		SrcFile:    "password from Keychain",
		SrcDefault: "default password from Keychain",
	} {
		if got := s.Note(); got != want {
			t.Errorf("%v: %q, want %q", s, got, want)
		}
	}
}
