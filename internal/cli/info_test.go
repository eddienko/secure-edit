package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eddienko/secure-edit/internal/vault"
)

func metaOfFile(t *testing.T, path string) vault.Meta {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := vault.ReadMeta(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNewFileGetsTitleAndDescription(t *testing.T) {
	useFakeStore(t)
	appendEditor(t, "hello")
	path := filepath.Join(t.TempDir(), "vpn")
	withStdin(t, "pw\n")
	if err := run([]string{"--custom", "--title", "VPN", "--description", "work vpn", path}); err != nil {
		t.Fatal(err)
	}
	if m := metaOfFile(t, path); m != (vault.Meta{Title: "VPN", Description: "work vpn"}) {
		t.Errorf("labels: %+v", m)
	}
	if got := decryptFile(t, path, "pw"); got != "hello\n" {
		t.Errorf("text: %q", got)
	}

	// Editing it again keeps them.
	appendEditor(t, "more")
	withStdin(t, "pw\n")
	if err := run([]string{path}); err != nil {
		t.Fatal(err)
	}
	if m := metaOfFile(t, path); m.Title != "VPN" {
		t.Errorf("labels lost on edit: %+v", m)
	}
	if got := decryptFile(t, path, "pw"); got != "hello\nmore\n" {
		t.Errorf("text: %q", got)
	}
}

func TestTitleIsRefusedOnAnExistingFile(t *testing.T) {
	path := makeSeditFile(t, "x")
	before, _ := os.ReadFile(path)
	err := run([]string{"--title", "T", path})
	if err == nil || !strings.Contains(err.Error(), "--set-info") {
		t.Fatalf("got %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("the file changed")
	}
}

func TestTitleIsValidatedBeforeAnythingHappens(t *testing.T) {
	useFakeStore(t)
	path := filepath.Join(t.TempDir(), "f")
	if err := run([]string{"--title", "a\x1bb", path}); err == nil {
		t.Error("a control character in the title was accepted")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a file was created")
	}
}

func TestEncryptAndWriteTakeTheLabelsToo(t *testing.T) {
	useFakeStore(t)
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	os.WriteFile(plain, []byte("data\n"), 0o600)
	withStdin(t, "pw\n")
	if err := run([]string{"--custom", "--title", "Notes", "--encrypt", "-y", plain}); err != nil {
		t.Fatal(err)
	}
	if m := metaOfFile(t, plain); m.Title != "Notes" {
		t.Errorf("--encrypt: %+v", m)
	}

	out := filepath.Join(dir, "piped")
	fakeTTY(t, "", "pw", "pw")
	withStdin(t, "from a pipe\n")
	if err := run([]string{"--custom", "--description", "piped in", "--write", out}); err != nil {
		t.Fatal(err)
	}
	if m := metaOfFile(t, out); m.Description != "piped in" {
		t.Errorf("--write: %+v", m)
	}
}

func TestSetInfoChangesOnlyWhatIsGiven(t *testing.T) {
	path := makeSeditFile(t, "keep me")
	withStdin(t, "pw\npw\npw\npw\n") // one line for each --set-info
	if err := run([]string{"--set-info", "--title", "First", path}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--set-info", "--description", "second", path}); err != nil {
		t.Fatal(err)
	}
	if m := metaOfFile(t, path); m != (vault.Meta{Title: "First", Description: "second"}) {
		t.Errorf("labels: %+v", m)
	}
	if err := run([]string{"--set-info", "--title", "", path}); err != nil { // clears the title
		t.Fatal(err)
	}
	if m := metaOfFile(t, path); m != (vault.Meta{Description: "second"}) {
		t.Errorf("after clearing the title: %+v", m)
	}
	if got := decryptFile(t, path, "pw"); got != "keep me\n" {
		t.Errorf("the text changed: %q", got)
	}
	if err := run([]string{"--set-info", "--description", "", path}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if data[6] != 1 {
		t.Errorf("a file with no labels should be version 1 again, got %d", data[6])
	}
}

func TestSetInfoWithTheWrongPasswordChangesNothing(t *testing.T) {
	path := makeSeditFile(t, "x")
	before, _ := os.ReadFile(path)
	withStdin(t, "wrong\nwrong\nwrong\nwrong\n")
	if err := run([]string{"--set-info", "--title", "T", path}); err == nil {
		t.Fatal("a wrong password was accepted")
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("the file changed")
	}
}

func TestInfoNeedsNoPassword(t *testing.T) {
	useFakeStore(t)
	path := filepath.Join(t.TempDir(), "f")
	if err := vault.SaveMeta(path, []byte("pw"), []byte("x"), nil, vault.Meta{Title: "Bank", Description: "a\nb"}); err != nil {
		t.Fatal(err)
	}
	noTTY(t)
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	err := run([]string{"--info", path})
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 1024)
	n, _ := r.Read(b)
	if got := string(b[:n]); got != "Title: Bank\nDescription: a\n  b\n" {
		t.Errorf("output %q", got)
	}
}

func TestArgumentsForTheLabels(t *testing.T) {
	for _, args := range [][]string{
		{"--title"},                 // no value
		{"--title", "T", "-p", "f"}, // not for printing
		{"--set-info", "f"},         // nothing to set
		{"--title", "T", "--share", "f", "--to", "age1x"},
	} {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	o, err := parseArgs([]string{"--title", "", "f"})
	if err != nil || o.title == nil || *o.title != "" {
		t.Errorf("an empty title must be kept as given: %+v, %v", o, err)
	}
}
