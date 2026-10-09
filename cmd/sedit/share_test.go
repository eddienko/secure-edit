package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"filippo.io/age/armor"
	"golang.org/x/crypto/ssh"
)

// A recipient: its public key as text and the identity that decrypts.
type testRecipient struct {
	public string
	id     age.Identity
}

func newAgeRecipient(t *testing.T) testRecipient {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return testRecipient{id.Recipient().String(), id}
}

func newHybridRecipient(t *testing.T) testRecipient {
	t.Helper()
	id, err := age.GenerateHybridIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return testRecipient{id.Recipient().String(), id}
}

func newSSHRecipient(t *testing.T) testRecipient {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	id, err := agessh.NewEd25519Identity(priv)
	if err != nil {
		t.Fatal(err)
	}
	return testRecipient{strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))), id}
}

// openAge decrypts age data (binary or armored) with id.
func openAge(t *testing.T, data []byte, id age.Identity) string {
	t.Helper()
	var src io.Reader = bytes.NewReader(data)
	if bytes.HasPrefix(data, []byte("-----BEGIN AGE ENCRYPTED FILE-----")) {
		src = armor.NewReader(src)
	}
	r, err := age.Decrypt(src, id)
	if err != nil {
		t.Fatalf("age decrypt: %v", err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// makeSeditFile creates a sedit file with the given text and password "pw".
func makeSeditFile(t *testing.T, text string) string {
	t.Helper()
	useFakeStore(t)
	appendEditor(t, text)
	path := filepath.Join(t.TempDir(), "secrets.txt")
	withStdin(t, "pw\n")
	if err := edit(path, false, choiceCustom); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustRecipients(t *testing.T, to ...string) []age.Recipient {
	t.Helper()
	recs, err := collectRecipients(to, nil)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func TestParseRecipient(t *testing.T) {
	for name, r := range map[string]testRecipient{"x25519": newAgeRecipient(t), "hybrid": newHybridRecipient(t), "ssh": newSSHRecipient(t)} {
		if _, err := parseRecipient(r.public); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := parseRecipient("  " + r.public + "\n"); err != nil {
			t.Errorf("%s with spaces: %v", name, err)
		}
	}
	secret := "AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ"
	for _, bad := range []string{"", "garbage", "age1notavalidkey", "ssh-ed25519 notbase64", "age-plugin-yubikey", secret, "-----BEGIN OPENSSH PRIVATE KEY-----"} {
		if _, err := parseRecipient(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	// A private key must never be echoed back in an error.
	_, err := collectRecipients([]string{secret}, nil)
	if err == nil || strings.Contains(err.Error(), "QQQQQQQQ") || !strings.Contains(err.Error(), "private key") {
		t.Errorf("private key error: %v", err)
	}
}

func TestReadRecipients(t *testing.T) {
	a, s := newAgeRecipient(t), newSSHRecipient(t)
	dir := t.TempDir()

	good := filepath.Join(dir, "recipients.txt")
	os.WriteFile(good, []byte("# my team\n\n"+a.public+"\n   \n"+s.public+" me@laptop\n"), 0o600)
	recs, err := readRecipients(good)
	if err != nil || len(recs) != 2 {
		t.Fatalf("got %d recipients, %v", len(recs), err)
	}

	bad := filepath.Join(dir, "bad.txt")
	os.WriteFile(bad, []byte(a.public+"\nnot a key\n"), 0o600)
	if _, err := readRecipients(bad); err == nil || !strings.Contains(err.Error(), "bad.txt:2") {
		t.Errorf("want file:line error, got %v", err)
	}

	empty := filepath.Join(dir, "empty.txt")
	os.WriteFile(empty, []byte("# nothing here\n"), 0o600)
	if _, err := collectRecipients(nil, []string{empty}); err == nil || !strings.Contains(err.Error(), "no recipients") {
		t.Errorf("empty file: %v", err)
	}
	if _, err := collectRecipients(nil, []string{filepath.Join(dir, "missing")}); err == nil {
		t.Error("missing file should fail")
	}
	if _, err := collectRecipients(nil, nil); err == nil {
		t.Error("no recipients should fail")
	}
}

func TestShareRoundTrip(t *testing.T) {
	path := makeSeditFile(t, "the secret")
	a, s := newAgeRecipient(t), newSSHRecipient(t)
	other := newAgeRecipient(t) // not a recipient

	out := filepath.Join(t.TempDir(), "shared.age")
	withStdin(t, "pw\n")
	if err := share(path, mustRecipients(t, a.public, s.public), out, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("the secret")) {
		t.Fatal("plaintext found in the shared file")
	}
	for name, r := range map[string]testRecipient{"x25519": a, "ssh": s} {
		if got := openAge(t, data, r.id); got != "the secret\n" {
			t.Errorf("%s recipient got %q", name, got)
		}
	}
	if _, err := age.Decrypt(bytes.NewReader(data), other.id); err == nil {
		t.Error("a non-recipient could decrypt")
	}
}

// Post-quantum (hybrid) recipients work, but age refuses to mix them with
// classic ones, and sedit must pass that on clearly.
func TestShareHybridRecipients(t *testing.T) {
	path := makeSeditFile(t, "pq secret")
	h1, h2 := newHybridRecipient(t), newHybridRecipient(t)
	out := filepath.Join(t.TempDir(), "pq.age")
	withStdin(t, "pw\n")
	if err := share(path, mustRecipients(t, h1.public, h2.public), out, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	for _, r := range []testRecipient{h1, h2} {
		if got := openAge(t, data, r.id); got != "pq secret\n" {
			t.Errorf("got %q", got)
		}
	}

	out2 := filepath.Join(t.TempDir(), "mixed.age")
	withStdin(t, "pw\n")
	err := share(path, mustRecipients(t, h1.public, newAgeRecipient(t).public), out2, false)
	if err == nil || !strings.Contains(err.Error(), "can't mix post-quantum and classic") {
		t.Fatalf("got %v", err)
	}
	if _, serr := os.Stat(out2); serr == nil {
		t.Error("output written despite the error")
	}
}

func TestShareDefaultOutputName(t *testing.T) {
	path := makeSeditFile(t, "x")
	r := newAgeRecipient(t)
	withStdin(t, "pw\n")
	if err := share(path, mustRecipients(t, r.public), "", false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path + ".age")
	if err != nil {
		t.Fatalf("default output not written: %v", err)
	}
	if openAge(t, data, r.id) != "x\n" {
		t.Error("wrong contents")
	}
}

func TestShareArmor(t *testing.T) {
	path := makeSeditFile(t, "armored")
	r := newAgeRecipient(t)
	out := filepath.Join(t.TempDir(), "a.age")
	withStdin(t, "pw\n")
	if err := share(path, mustRecipients(t, r.public), out, true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	if !bytes.HasPrefix(data, []byte("-----BEGIN AGE ENCRYPTED FILE-----")) {
		t.Fatalf("not armored: %.40q", data)
	}
	if got := openAge(t, data, r.id); got != "armored\n" {
		t.Errorf("got %q", got)
	}
}

func TestShareToStdout(t *testing.T) {
	path := makeSeditFile(t, "to stdout")
	r := newAgeRecipient(t)

	tmp, _ := os.CreateTemp(t.TempDir(), "stdout")
	old := os.Stdout
	os.Stdout = tmp
	t.Cleanup(func() { os.Stdout = old })
	withStdin(t, "pw\n")
	err := share(path, mustRecipients(t, r.public), "-", true)
	os.Stdout = old
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(tmp.Name())
	if got := openAge(t, data, r.id); got != "to stdout\n" {
		t.Errorf("got %q", got)
	}
	// "-o -" must not have created a file called "-".
	if _, err := os.Stat("-"); err == nil {
		t.Error(`a file named "-" was created`)
	}
}

// --share must never overwrite the sedit file it reads from.
func TestShareRefusesToOverwriteSource(t *testing.T) {
	path := makeSeditFile(t, "keep me")
	before, _ := os.ReadFile(path)
	r := newAgeRecipient(t)
	recs := mustRecipients(t, r.public)

	link := filepath.Join(filepath.Dir(path), "link")
	os.Symlink(path, link)
	hard := filepath.Join(filepath.Dir(path), "hard")
	os.Link(path, hard)

	for name, out := range map[string]string{
		"same path":      path,
		"symlink to it":  link,
		"hard link":      hard,
		"different form": filepath.Join(filepath.Dir(path), ".", filepath.Base(path)),
	} {
		withStdin(t, "pw\n")
		err := share(path, recs, out, false)
		if err == nil || !strings.Contains(err.Error(), "is the file being shared") {
			t.Errorf("%s: got %v", name, err)
		}
		if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
			t.Fatalf("%s: the sedit file was modified", name)
		}
	}
	withStdin(t, "pw\n")
	if got := decryptFile(t, path, "pw"); got != "keep me\n" {
		t.Errorf("file contents now %q", got)
	}
}

func TestShareRejectsNonSeditFile(t *testing.T) {
	useFakeStore(t)
	path := filepath.Join(t.TempDir(), "plain.txt")
	os.WriteFile(path, []byte("not encrypted"), 0o600)
	r := newAgeRecipient(t)
	withStdin(t, "") // must fail before asking for a password
	err := share(path, mustRecipients(t, r.public), filepath.Join(t.TempDir(), "o.age"), false)
	if err == nil || !strings.Contains(err.Error(), "not a sedit file") {
		t.Fatalf("got %v", err)
	}
}

func TestShareWrongPassword(t *testing.T) {
	path := makeSeditFile(t, "x")
	r := newAgeRecipient(t)
	out := filepath.Join(t.TempDir(), "o.age")
	withStdin(t, "wrong\n")
	if err := share(path, mustRecipients(t, r.public), out, false); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("output written despite the wrong password")
	}
}

// Sharing is read-only and in memory: no temp files, no changes to the source.
func TestShareLeavesNothingElseBehind(t *testing.T) {
	path := makeSeditFile(t, "x")
	dir := filepath.Dir(path)
	r := newAgeRecipient(t)
	list := func() string {
		es, _ := os.ReadDir(dir)
		var names []string
		for _, e := range es {
			names = append(names, e.Name())
		}
		return strings.Join(names, " ")
	}
	before := list()
	withStdin(t, "pw\n")
	if err := share(path, mustRecipients(t, r.public), "", false); err != nil {
		t.Fatal(err)
	}
	if got, want := list(), before+" "+filepath.Base(path)+".age"; !sameWords(got, want) {
		t.Errorf("directory is %q, want %q", got, want)
	}
}

func sameWords(a, b string) bool {
	m := map[string]int{}
	for _, w := range strings.Fields(a) {
		m[w]++
	}
	for _, w := range strings.Fields(b) {
		m[w]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return true
}
