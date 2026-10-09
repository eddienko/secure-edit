package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"filippo.io/age"
)

// publicKeyOf reads the "# public key:" line from an identity file.
func publicKeyOf(t *testing.T, data string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^# public key: (\S+)$`).FindStringSubmatch(data)
	if m == nil {
		t.Fatalf("no public key line in %q", data)
	}
	return m[1]
}

func TestKeygenWritesUsableKeyFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "key.txt")
	stderr := captureStderr(t, func() {
		if err := keygen(out, false); err != nil {
			t.Fatal(err)
		}
	})

	fi, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	data, _ := os.ReadFile(out)
	text := string(data)
	if !strings.HasPrefix(text, "# created: ") || !strings.Contains(text, "\nAGE-SECRET-KEY-1") {
		t.Errorf("unexpected format: %q", text)
	}

	// The file parses as identities, and its comment is the matching public key.
	ids, err := age.ParseIdentities(bytes.NewReader(data))
	if err != nil || len(ids) != 1 {
		t.Fatalf("ParseIdentities: %d, %v", len(ids), err)
	}
	pub := publicKeyOf(t, text)
	if want := ids[0].(*age.X25519Identity).Recipient().String(); pub != want {
		t.Errorf("public key comment %q != %q", pub, want)
	}
	if _, err := parseRecipient(pub); err != nil {
		t.Errorf("sedit can't use its own public key: %v", err)
	}
	// What the user sees: the public key, never the private one.
	if !strings.Contains(stderr, "Public key: "+pub) || strings.Contains(stderr, "AGE-SECRET-KEY") {
		t.Errorf("stderr: %q", stderr)
	}
}

func TestKeygenPostQuantum(t *testing.T) {
	out := filepath.Join(t.TempDir(), "pq.txt")
	captureStderr(t, func() {
		if err := keygen(out, true); err != nil {
			t.Fatal(err)
		}
	})
	data, _ := os.ReadFile(out)
	if !strings.Contains(string(data), "\nAGE-SECRET-KEY-PQ-1") || !strings.HasPrefix(publicKeyOf(t, string(data)), "age1pq1") {
		t.Errorf("not a hybrid key: %q", data)
	}
	if _, err := age.ParseIdentities(bytes.NewReader(data)); err != nil {
		t.Error(err)
	}
}

func TestKeygenKeysAreUnique(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	captureStderr(t, func() {
		keygen(a, false)
		keygen(b, false)
	})
	da, _ := os.ReadFile(a)
	db, _ := os.ReadFile(b)
	if publicKeyOf(t, string(da)) == publicKeyOf(t, string(db)) {
		t.Error("two keygens gave the same key")
	}
}

func TestKeygenNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	existing := writeFile(t, filepath.Join(dir, "key.txt"), []byte("precious"))
	link := filepath.Join(dir, "link")
	os.Symlink(existing, link)
	dangling := filepath.Join(dir, "dangling")
	os.Symlink(filepath.Join(dir, "nowhere"), dangling)

	for name, out := range map[string]string{"existing file": existing, "symlink": link, "dangling symlink": dangling} {
		err := keygen(out, false)
		if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if b, _ := os.ReadFile(existing); string(b) != "precious" {
		t.Errorf("existing file now %q", b)
	}
	notExist(t, filepath.Join(dir, "nowhere")) // the dangling link wasn't written through
}

func TestKeygenMissingDirectory(t *testing.T) {
	out := filepath.Join(t.TempDir(), "no", "such", "dir", "key.txt")
	if err := keygen(out, false); err == nil {
		t.Fatal("expected an error")
	}
	notExist(t, out)
}

func TestKeygenToStdout(t *testing.T) {
	dir := t.TempDir()
	tmp, _ := os.CreateTemp(dir, "stdout")
	old := os.Stdout
	os.Stdout = tmp
	t.Cleanup(func() { os.Stdout = old })
	var stderr string
	err := func() error {
		var e error
		stderr = captureStderr(t, func() { e = keygen("-", false) })
		return e
	}()
	os.Stdout = old
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(tmp.Name())
	if _, err := age.ParseIdentities(bytes.NewReader(data)); err != nil {
		t.Errorf("stdout is not a key file: %v", err)
	}
	if !strings.Contains(stderr, "written to stdout") || !strings.Contains(stderr, "Public key: age1") {
		t.Errorf("stderr: %q", stderr)
	}
	notExist(t, "-")
}

// A generated key must work for the whole sharing story.
func TestKeygenKeyWorksWithShareAndImport(t *testing.T) {
	for name, pq := range map[string]bool{"x25519": false, "post-quantum": true} {
		path := makeSeditFile(t, "for "+name)
		dir := t.TempDir()
		keyFile := filepath.Join(dir, "key.txt")
		captureStderr(t, func() {
			if err := keygen(keyFile, pq); err != nil {
				t.Fatal(err)
			}
		})
		data, _ := os.ReadFile(keyFile)
		shared := filepath.Join(dir, "shared.age")

		withStdin(t, "pw\n")
		if err := share(path, mustRecipients(t, publicKeyOf(t, string(data))), shared, false); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		withStdin(t, "theirpw\n")
		if err := importFile(shared, []string{keyFile}, "", choiceCustom); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := decryptFile(t, filepath.Join(dir, "shared"), "theirpw"); got != "for "+name+"\n" {
			t.Errorf("%s: got %q", name, got)
		}
	}
}
