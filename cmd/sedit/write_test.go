package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTTY stands in for /dev/tty. line is what the terminal would let a
// "[d]efault or [c]ustom?" question read; secrets are the hidden answers to
// the password prompts, in order. It returns how many secrets were asked for.
func fakeTTY(t *testing.T, line string, secrets ...string) *int {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "tty")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(line)
	f.Seek(0, 0)
	asked := 0
	oldOpen, oldRead := openTTY, readSecret
	openTTY = func() (*os.File, error) { return f, nil }
	readSecret = func(int) ([]byte, error) {
		if asked >= len(secrets) {
			return nil, errors.New("test: more passwords asked for than provided")
		}
		asked++
		return []byte(secrets[asked-1]), nil
	}
	t.Cleanup(func() { openTTY, readSecret = oldOpen, oldRead; f.Close() })
	return &asked
}

// noTTY makes /dev/tty unavailable, and fails the test if anything asks.
func noTTY(t *testing.T) {
	t.Helper()
	oldOpen, oldRead := openTTY, readSecret
	openTTY = func() (*os.File, error) { return nil, errors.New("no tty") }
	readSecret = func(int) ([]byte, error) {
		t.Error("a password was asked for on a terminal")
		return nil, errors.New("x")
	}
	t.Cleanup(func() { openTTY, readSecret = oldOpen, oldRead })
}

func TestWriteCreatesNewFile(t *testing.T) {
	useFakeStore(t)
	asked := fakeTTY(t, "", "pw", "pw") // new password, and its confirmation
	path := filepath.Join(t.TempDir(), "secret.txt")
	withStdin(t, "my secret\n")
	out := captureStderr(t, func() {
		if err := writeStdin(path, false, choiceAsk); err != nil {
			t.Fatal(err)
		}
	})
	if got := decryptFile(t, path, "pw"); got != "my secret\n" {
		t.Errorf("got %q", got)
	}
	if *asked != 2 {
		t.Errorf("asked for %d passwords, want 2 (new + confirm)", *asked)
	}
	if !strings.Contains(out, "created "+path+" (encrypted)") {
		t.Errorf("message: %q", out)
	}
	if data, _ := os.ReadFile(path); bytes.Contains(data, []byte("my secret")) {
		t.Error("plaintext in the file")
	}
}

func TestWriteReplacesAndKeepsBackup(t *testing.T) {
	path := makeSeditFile(t, "old text") // password "pw"
	fakeTTY(t, "", "pw")
	withStdin(t, "new text\n")
	if err := writeStdin(path, false, choiceAsk); err != nil {
		t.Fatal(err)
	}
	if got := decryptFile(t, path, "pw"); got != "new text\n" {
		t.Errorf("got %q", got)
	}
	if got := decryptFile(t, backupPath(path), "pw"); got != "old text\n" {
		t.Errorf("backup holds %q", got)
	}
}

func TestAppend(t *testing.T) {
	path := makeSeditFile(t, "line one") // "line one\n"
	fakeTTY(t, "", "pw")
	withStdin(t, "line two\n")
	if err := writeStdin(path, true, choiceAsk); err != nil {
		t.Fatal(err)
	}
	if got := decryptFile(t, path, "pw"); got != "line one\nline two\n" {
		t.Errorf("got %q", got)
	}
	if got := decryptFile(t, backupPath(path), "pw"); got != "line one\n" {
		t.Errorf("backup holds %q", got)
	}

	// Appending to a file that doesn't exist yet creates it.
	fresh := filepath.Join(t.TempDir(), "fresh.txt")
	fakeTTY(t, "", "pw2", "pw2")
	withStdin(t, "first\n")
	if err := writeStdin(fresh, true, choiceAsk); err != nil {
		t.Fatal(err)
	}
	if got := decryptFile(t, fresh, "pw2"); got != "first\n" {
		t.Errorf("got %q", got)
	}
}

// Raw bytes in, raw bytes out: no newline added, binary data intact.
func TestWriteIsByteExact(t *testing.T) {
	useFakeStore(t)
	fakeTTY(t, "", "pw", "pw")
	path := filepath.Join(t.TempDir(), "bin")
	data := "no trailing newline\x00\x01\xff and \r\n bytes"
	withStdin(t, data)
	if err := writeStdin(path, false, choiceAsk); err != nil {
		t.Fatal(err)
	}
	if got := decryptFile(t, path, "pw"); got != data {
		t.Errorf("got %q, want %q", got, data)
	}
}

// With the password in the Keychain nothing needs a terminal at all.
func TestWriteUsesKeychainWithoutTerminal(t *testing.T) {
	fs := useFakeStore(t)
	appendEditor(t, "one")
	path := filepath.Join(t.TempDir(), "secrets")
	withStdin(t, "pw\n")
	if err := edit(path, true, choiceCustom); err != nil { // creates and remembers
		t.Fatal(err)
	}
	if len(fs.m) == 0 {
		t.Fatal("password not remembered")
	}

	noTTY(t)
	withStdin(t, "piped\n")
	out := captureStderr(t, func() {
		if err := writeStdin(path, true, choiceAsk); err != nil {
			t.Fatal(err)
		}
	})
	if got := decryptFile(t, path, "pw"); got != "one\npiped\n" {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(out, "password from Keychain") {
		t.Errorf("message: %q", out)
	}
}

func TestWriteNewFileWithDefaultNeedsNoTerminal(t *testing.T) {
	fs := useFakeStore(t)
	fs.m[defaultAccount] = []byte("dpw")
	noTTY(t)
	path := filepath.Join(t.TempDir(), "d.txt")
	withStdin(t, "with the default\n")
	if err := writeStdin(path, false, choiceDefault); err != nil {
		t.Fatal(err)
	}
	if got := decryptFile(t, path, "dpw"); got != "with the default\n" {
		t.Errorf("got %q", got)
	}
}

// The default/custom question is asked on the terminal, not on the pipe.
func TestWriteAsksDefaultOrCustomOnTheTerminal(t *testing.T) {
	fs := useFakeStore(t)
	fs.m[defaultAccount] = []byte("dpw")
	path := filepath.Join(t.TempDir(), "q.txt")
	asked := fakeTTY(t, "c\n", "cpw", "cpw") // answers "custom", then types the password twice
	withStdin(t, "data\n")
	if err := writeStdin(path, false, choiceAsk); err != nil {
		t.Fatal(err)
	}
	if got := decryptFile(t, path, "cpw"); got != "data\n" || *asked != 2 {
		t.Errorf("got %q after %d prompts", got, *asked)
	}
}

func TestWriteRefusesEmptyInput(t *testing.T) {
	path := makeSeditFile(t, "precious")
	before, _ := os.ReadFile(path)
	noTTY(t)
	for _, appendMode := range []bool{false, true} {
		withStdin(t, "")
		err := writeStdin(path, appendMode, choiceAsk)
		if err == nil || !strings.Contains(err.Error(), "nothing was piped") {
			t.Errorf("append=%v: got %v", appendMode, err)
		}
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Error("the file was modified")
	}
	notExist(t, backupPath(path))
}

func TestWriteRefusesTerminalStdin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x")
	old := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	defer func() { stdinIsTerminal = old }()
	err := writeStdin(path, false, choiceAsk)
	if err == nil || !strings.Contains(err.Error(), "pipe it in") {
		t.Fatalf("got %v", err)
	}
	notExist(t, path)
}

func TestWriteWithoutAnyWayToAsk(t *testing.T) {
	path := makeSeditFile(t, "keep") // no Keychain entry (fake store is empty)
	before, _ := os.ReadFile(path)
	noTTY(t)
	withStdin(t, "new\n")
	err := writeStdin(path, false, choiceAsk)
	if err == nil || !strings.Contains(err.Error(), "no terminal") || !strings.Contains(err.Error(), "--remember") {
		t.Fatalf("got %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Error("the file was modified")
	}
}

func TestWriteRejectsNonSeditFileBeforeAsking(t *testing.T) {
	useFakeStore(t)
	noTTY(t)
	path := filepath.Join(t.TempDir(), "plain.txt")
	os.WriteFile(path, []byte("not encrypted"), 0o600)
	withStdin(t, "data\n")
	err := writeStdin(path, false, choiceAsk)
	if err == nil || !strings.Contains(err.Error(), "not a sedit file") {
		t.Fatalf("got %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "not encrypted" {
		t.Error("the file was modified")
	}
}

func TestWriteWrongPassword(t *testing.T) {
	path := makeSeditFile(t, "keep")
	before, _ := os.ReadFile(path)
	fakeTTY(t, "", "wrong")
	withStdin(t, "new\n")
	if err := writeStdin(path, false, choiceAsk); err == nil {
		t.Fatal("expected an error")
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Error("the file was modified")
	}
}

func TestWriteWritesThroughASymlinkViaRun(t *testing.T) {
	path := makeSeditFile(t, "target")
	link := filepath.Join(filepath.Dir(path), "link")
	os.Symlink(path, link)
	fakeTTY(t, "", "pw")
	withStdin(t, "added\n")
	if err := run([]string{"--append", link}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced")
	}
	if got := decryptFile(t, path, "pw"); got != "target\nadded\n" {
		t.Errorf("got %q", got)
	}
}

// The trap from before: piping text into plain "sedit FILE" uses it as the
// password. That still works (scripting), but must now say so.
func TestPlainEditWithPipedStdinWarns(t *testing.T) {
	useFakeStore(t)
	appendEditor(t, "x")
	path := filepath.Join(t.TempDir(), "new.txt")
	withStdin(t, "my secret\n")
	out := captureStderr(t, func() {
		if err := edit(path, false, choiceAsk); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"stdin is not a terminal", "--write"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	// ...and an existing file doesn't get the warning, only a new one does.
	withStdin(t, "my secret\n")
	out = captureStderr(t, func() {
		if err := edit(path, false, choiceAsk); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "stdin is not a terminal") {
		t.Errorf("warned for an existing file: %q", out)
	}
	// A new file whose password comes from elsewhere isn't a surprise either.
	fs := useFakeStore(t)
	fs.m[defaultAccount] = []byte("dpw")
	out = captureStderr(t, func() {
		withStdin(t, "")
		if err := edit(filepath.Join(t.TempDir(), "d.txt"), false, choiceDefault); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "stdin is not a terminal") {
		t.Errorf("warned although stdin wasn't used: %q", out)
	}
}
