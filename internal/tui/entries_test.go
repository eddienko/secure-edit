package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eddienko/secure-edit/internal/vault"
)

var fastKDF = vault.NewKDFParams(1, 64, 1)

// seditFile writes a real, cheaply encrypted sedit file.
func seditFile(t *testing.T, dir, name, password, text string) string {
	t.Helper()
	data, err := vault.Encrypt([]byte(password), []byte(text), fastKDF)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func plainFile(t *testing.T, dir, name, text string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestKindOf(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]Kind{
		seditFile(t, dir, "a", "pw", "x"):                                       KindSedit,
		plainFile(t, dir, "b", "age-encryption.org/v1\n-> X25519 abc\n"):        KindAge,
		plainFile(t, dir, "c", "-----BEGIN AGE ENCRYPTED FILE-----\nabc\n"):     KindAge,
		plainFile(t, dir, "d", "\n\n-----BEGIN AGE ENCRYPTED FILE-----\nabc\n"): KindAge,
		plainFile(t, dir, "e", "just text"):                                     KindOther,
		plainFile(t, dir, "f", ""):                                              KindOther,
		plainFile(t, dir, "g", "SEDIT"):                                         KindOther, // magic is "SEDIT\x00"
		filepath.Join(dir, "missing"):                                           KindOther,
	}
	for path, want := range cases {
		if got := KindOf(path); got != want {
			t.Errorf("%s: got %v, want %v", filepath.Base(path), got, want)
		}
	}
}

func TestListSortsAndHides(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "banana.txt", "pw", "x")
	seditFile(t, dir, "Apple.txt", "pw", "x")
	seditFile(t, dir, "apple.txt.bak", "pw", "old") // hidden: a backup
	plainFile(t, dir, ".secret.lock", "")           // hidden: a dot file
	plainFile(t, dir, ".sedit-12345", "")           // hidden: sedit's own temp file
	plainFile(t, dir, "notes.md", "plain")
	plainFile(t, dir, "ages.age", "age-encryption.org/v1\n")
	os.Mkdir(filepath.Join(dir, "zeta"), 0o700)
	os.Mkdir(filepath.Join(dir, "Alpha"), 0o700)
	os.Mkdir(filepath.Join(dir, ".git"), 0o700)

	es, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		name string
		kind Kind
	}
	var got []row
	for _, e := range es {
		got = append(got, row{e.Name, e.Kind})
	}
	want := []row{
		{"Alpha", KindDir}, {"zeta", KindDir}, // directories first
		{"Apple.txt", KindSedit}, {"ages.age", KindAge}, {"banana.txt", KindSedit}, {"notes.md", KindOther},
	}
	// Case-insensitive: ages.age < Apple.txt ("ag" < "ap").
	want = []row{
		{"Alpha", KindDir}, {"zeta", KindDir},
		{"ages.age", KindAge}, {"Apple.txt", KindSedit}, {"banana.txt", KindSedit}, {"notes.md", KindOther},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %v, want %v", i, got[i], want[i])
		}
	}
	for _, e := range es {
		if e.Path != filepath.Join(dir, e.Name) {
			t.Errorf("%s: path %q", e.Name, e.Path)
		}
	}
}

func TestListFollowsSymlinksAndToleratesDanglingOnes(t *testing.T) {
	dir := t.TempDir()
	real := seditFile(t, dir, "real.txt", "pw", "x")
	os.Symlink(real, filepath.Join(dir, "link.txt"))
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o700)
	os.Symlink(sub, filepath.Join(dir, "linkdir"))
	os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "dangling"))

	es, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]Kind{}
	for _, e := range es {
		kinds[e.Name] = e.Kind
	}
	if kinds["link.txt"] != KindSedit || kinds["linkdir"] != KindDir || kinds["dangling"] != KindOther {
		t.Errorf("kinds: %v", kinds)
	}
}

func TestListMissingDirectory(t *testing.T) {
	if _, err := List(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected an error")
	}
}
