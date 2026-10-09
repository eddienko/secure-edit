package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/armor"
	"golang.org/x/crypto/ssh"
)

func writeFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// ageEncrypt encrypts text to the recipients, binary or armored.
func ageEncrypt(t *testing.T, text string, armored bool, recips ...age.Recipient) []byte {
	t.Helper()
	var buf bytes.Buffer
	var dst = &buf
	var aw interface {
		Write([]byte) (int, error)
		Close() error
	}
	if armored {
		aw = armor.NewWriter(dst)
	}
	var out interface {
		Write([]byte) (int, error)
		Close() error
	}
	var err error
	if armored {
		out, err = age.Encrypt(aw, recips...)
	} else {
		out, err = age.Encrypt(dst, recips...)
	}
	if err != nil {
		t.Fatal(err)
	}
	out.Write([]byte(text))
	out.Close()
	if aw != nil {
		aw.Close()
	}
	return buf.Bytes()
}

// sshKey makes an ed25519 SSH key; the private key is a PEM file, protected by
// passphrase if it is not empty. It returns the public key line and PEM path.
func sshKey(t *testing.T, dir, passphrase string) (pubLine, pemPath string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	sshPub, _ := ssh.NewPublicKey(pub)
	pemPath = writeFile(t, filepath.Join(dir, "id_ed25519"), pem.EncodeToMemory(block))
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))), pemPath
}

func recipient(t *testing.T, line string) age.Recipient {
	t.Helper()
	r, err := parseRecipient(line)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func notExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Errorf("%s exists", path)
	}
}

func TestImportAgeKey(t *testing.T) {
	useFakeStore(t)
	dir := t.TempDir()
	id, _ := age.GenerateX25519Identity()
	idFile := writeFile(t, filepath.Join(dir, "key.txt"), []byte("# created: today\n# public key: "+id.Recipient().String()+"\n"+id.String()+"\n"))
	in := writeFile(t, filepath.Join(dir, "notes.txt.age"), ageEncrypt(t, "shared text\n", false, id.Recipient()))
	before, _ := os.ReadFile(in)

	withStdin(t, "newpw\n")
	if err := importFile(in, []string{idFile}, "", choiceCustom); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "notes.txt") // the .age is stripped
	if got := decryptFile(t, out, "newpw"); got != "shared text\n" {
		t.Errorf("got %q", got)
	}
	if fi, _ := os.Stat(out); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	if after, _ := os.ReadFile(in); !bytes.Equal(before, after) {
		t.Error("the age file was modified")
	}
	if data, _ := os.ReadFile(out); bytes.Contains(data, []byte("shared text")) {
		t.Error("plaintext in the sedit file")
	}
}

func TestImportArmoredAndExplicitOutput(t *testing.T) {
	useFakeStore(t)
	dir := t.TempDir()
	id, _ := age.GenerateX25519Identity()
	idFile := writeFile(t, filepath.Join(dir, "key.txt"), []byte(id.String()+"\n"))
	in := writeFile(t, filepath.Join(dir, "msg.asc"), ageEncrypt(t, "armored!\n", true, id.Recipient()))
	out := filepath.Join(dir, "chosen.txt")

	withStdin(t, "newpw\n")
	if err := importFile(in, []string{idFile}, out, choiceCustom); err != nil {
		t.Fatal(err)
	}
	if got := decryptFile(t, out, "newpw"); got != "armored!\n" {
		t.Errorf("got %q", got)
	}
}

func TestImportHybridKey(t *testing.T) {
	useFakeStore(t)
	dir := t.TempDir()
	id, _ := age.GenerateHybridIdentity()
	idFile := writeFile(t, filepath.Join(dir, "pq.txt"), []byte(id.String()+"\n"))
	in := writeFile(t, filepath.Join(dir, "pq.age"), ageEncrypt(t, "post-quantum\n", false, id.Recipient()))
	withStdin(t, "newpw\n")
	if err := importFile(in, []string{idFile}, "", choiceCustom); err != nil {
		t.Fatal(err)
	}
	if got := decryptFile(t, filepath.Join(dir, "pq"), "newpw"); got != "post-quantum\n" {
		t.Errorf("got %q", got)
	}
}

func TestImportSSHKey(t *testing.T) {
	useFakeStore(t)
	dir := t.TempDir()
	pub, pemPath := sshKey(t, dir, "")
	in := writeFile(t, filepath.Join(dir, "s.age"), ageEncrypt(t, "via ssh\n", false, recipient(t, pub)))
	withStdin(t, "newpw\n") // no passphrase asked: only the new password
	if err := importFile(in, []string{pemPath}, "", choiceCustom); err != nil {
		t.Fatal(err)
	}
	if got := decryptFile(t, filepath.Join(dir, "s"), "newpw"); got != "via ssh\n" {
		t.Errorf("got %q", got)
	}
}

func TestImportPassphraseProtectedSSHKey(t *testing.T) {
	useFakeStore(t)
	dir := t.TempDir()
	pub, pemPath := sshKey(t, dir, "keypass")
	in := writeFile(t, filepath.Join(dir, "s.age"), ageEncrypt(t, "locked key\n", false, recipient(t, pub)))

	withStdin(t, "keypass\nnewpw\n") // key passphrase, then the new password
	if err := importFile(in, []string{pemPath}, "", choiceCustom); err != nil {
		t.Fatal(err)
	}
	if got := decryptFile(t, filepath.Join(dir, "s"), "newpw"); got != "locked key\n" {
		t.Errorf("got %q", got)
	}

	// Wrong key passphrase: fails, writes nothing.
	in2 := writeFile(t, filepath.Join(dir, "t.age"), ageEncrypt(t, "x", false, recipient(t, pub)))
	withStdin(t, "wrong\nnewpw\n")
	if err := importFile(in2, []string{pemPath}, "", choiceCustom); err == nil {
		t.Error("wrong passphrase accepted")
	}
	notExist(t, filepath.Join(dir, "t"))
}

func TestImportPassphraseFile(t *testing.T) {
	useFakeStore(t)
	dir := t.TempDir()
	rec, _ := age.NewScryptRecipient("sharepass")
	rec.SetWorkFactor(10) // fast for tests
	in := writeFile(t, filepath.Join(dir, "p.age"), ageEncrypt(t, "by passphrase\n", false, rec))

	withStdin(t, "sharepass\nnewpw\n")
	if err := importFile(in, nil, "", choiceCustom); err != nil {
		t.Fatal(err)
	}
	if got := decryptFile(t, filepath.Join(dir, "p"), "newpw"); got != "by passphrase\n" {
		t.Errorf("got %q", got)
	}

	in2 := writeFile(t, filepath.Join(dir, "q.age"), ageEncrypt(t, "x", false, rec))
	withStdin(t, "badpass\nnewpw\n")
	if err := importFile(in2, nil, "", choiceCustom); err == nil || !strings.Contains(err.Error(), "wrong passphrase") {
		t.Errorf("wrong passphrase: got %v", err)
	}
	notExist(t, filepath.Join(dir, "q"))

	// Giving a key for a passphrase file points at the right way to import it.
	id, _ := age.GenerateX25519Identity()
	idFile := writeFile(t, filepath.Join(dir, "key.txt"), []byte(id.String()+"\n"))
	withStdin(t, "")
	if err := importFile(in2, []string{idFile}, "", choiceCustom); err == nil || !strings.Contains(err.Error(), "without -i") {
		t.Errorf("key for a passphrase file: got %v", err)
	}
}

// A key-encrypted file without -i must say what to do, not ask for a passphrase.
func TestImportKeyFileWithoutIdentity(t *testing.T) {
	useFakeStore(t)
	dir := t.TempDir()
	id, _ := age.GenerateX25519Identity()
	in := writeFile(t, filepath.Join(dir, "k.age"), ageEncrypt(t, "x", false, id.Recipient()))
	withStdin(t, "")
	err := importFile(in, nil, "", choiceCustom)
	if err == nil || !strings.Contains(err.Error(), "-i KEY") {
		t.Fatalf("got %v", err)
	}
	notExist(t, filepath.Join(dir, "k"))
}

func TestImportWrongKeyWritesNothing(t *testing.T) {
	useFakeStore(t)
	dir := t.TempDir()
	mine, _ := age.GenerateX25519Identity()
	theirs, _ := age.GenerateX25519Identity()
	idFile := writeFile(t, filepath.Join(dir, "mine.txt"), []byte(mine.String()+"\n"))
	in := writeFile(t, filepath.Join(dir, "x.age"), ageEncrypt(t, "not for me", false, theirs.Recipient()))
	withStdin(t, "") // must fail before asking for a new password
	err := importFile(in, []string{idFile}, "", choiceCustom)
	if err == nil || !strings.Contains(err.Error(), "none of the given keys fit") {
		t.Fatalf("got %v", err)
	}
	notExist(t, filepath.Join(dir, "x"))
}

func TestImportRefusals(t *testing.T) {
	useFakeStore(t)
	dir := t.TempDir()
	id, _ := age.GenerateX25519Identity()
	idFile := writeFile(t, filepath.Join(dir, "key.txt"), []byte(id.String()+"\n"))
	in := writeFile(t, filepath.Join(dir, "a.age"), ageEncrypt(t, "x", false, id.Recipient()))
	noSuffix := writeFile(t, filepath.Join(dir, "plain"), ageEncrypt(t, "x", false, id.Recipient()))
	ids := []string{idFile}

	existing := writeFile(t, filepath.Join(dir, "a"), []byte("precious"))
	withStdin(t, "newpw\n")
	if err := importFile(in, ids, "", choiceCustom); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("existing output: %v", err)
	}
	if b, _ := os.ReadFile(existing); string(b) != "precious" {
		t.Error("existing file was overwritten")
	}

	link := filepath.Join(dir, "linked")
	os.Symlink(existing, link)
	if err := importFile(in, ids, link, choiceCustom); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("symlink output: %v", err)
	}

	if err := importFile(in, ids, in, choiceCustom); err == nil || !strings.Contains(err.Error(), "being imported") {
		t.Errorf("output = input: %v", err)
	}
	if err := importFile(noSuffix, ids, "", choiceCustom); err == nil || !strings.Contains(err.Error(), "-o OUT") {
		t.Errorf("no .age suffix: %v", err)
	}
	if err := importFile(in, ids, "-", choiceCustom); err == nil {
		t.Error("-o - accepted")
	}
	if err := importFile(in, []string{filepath.Join(dir, "missing")}, filepath.Join(dir, "o1"), choiceCustom); err == nil {
		t.Error("missing identity file accepted")
	}
	if err := importFile(filepath.Join(dir, "absent.age"), ids, filepath.Join(dir, "o2"), choiceCustom); err == nil {
		t.Error("missing input accepted")
	}
	notExist(t, filepath.Join(dir, "o1"))
	notExist(t, filepath.Join(dir, "o2"))
}

func TestImportBadInputs(t *testing.T) {
	useFakeStore(t)
	dir := t.TempDir()
	id, _ := age.GenerateX25519Identity()
	idFile := writeFile(t, filepath.Join(dir, "key.txt"), []byte(id.String()+"\n"))

	// Not an age file at all.
	junk := writeFile(t, filepath.Join(dir, "junk.age"), []byte("hello, this is not encrypted"))
	if err := importFile(junk, []string{idFile}, "", choiceCustom); err == nil || !strings.Contains(err.Error(), "not a usable age file") {
		t.Errorf("junk: %v", err)
	}

	// Truncated or tampered payloads must fail without writing anything.
	good := ageEncrypt(t, strings.Repeat("secret data ", 200), false, id.Recipient())
	for name, data := range map[string][]byte{
		"truncated": good[:len(good)-20],
		"tampered":  append(bytes.Clone(good[:len(good)-1]), good[len(good)-1]^1),
	} {
		in := writeFile(t, filepath.Join(dir, name+".age"), data)
		withStdin(t, "newpw\n")
		if err := importFile(in, []string{idFile}, "", choiceCustom); err == nil {
			t.Errorf("%s: no error", name)
		}
		notExist(t, filepath.Join(dir, name))
	}

	// An identity file with something that isn't a key; never echo its content.
	bad := writeFile(t, filepath.Join(dir, "bad.txt"), []byte("AGE-SECRET-KEY-NOT-REALLY-xyzzy\n"))
	in := writeFile(t, filepath.Join(dir, "ok.age"), ageEncrypt(t, "x", false, id.Recipient()))
	err := importFile(in, []string{bad}, "", choiceCustom)
	if err == nil || strings.Contains(err.Error(), "xyzzy") {
		t.Errorf("bad identity file: %v", err)
	}
}

func TestImportUsesDefaultPassword(t *testing.T) {
	fs := useFakeStore(t)
	fs.m[defaultAccount] = []byte("dpw")
	dir := t.TempDir()
	id, _ := age.GenerateX25519Identity()
	idFile := writeFile(t, filepath.Join(dir, "key.txt"), []byte(id.String()+"\n"))
	in := writeFile(t, filepath.Join(dir, "d.age"), ageEncrypt(t, "x\n", false, id.Recipient()))
	withStdin(t, "")
	if err := importFile(in, []string{idFile}, "", choiceDefault); err != nil {
		t.Fatal(err)
	}
	decryptFile(t, filepath.Join(dir, "d"), "dpw")
}

// The whole sharing story: share a sedit file, then import it elsewhere.
func TestShareThenImportRoundTrip(t *testing.T) {
	path := makeSeditFile(t, "round trip")
	dir := t.TempDir()
	id, _ := age.GenerateX25519Identity()
	idFile := writeFile(t, filepath.Join(dir, "key.txt"), []byte(id.String()+"\n"))
	shared := filepath.Join(dir, "shared.age")

	withStdin(t, "pw\n")
	if err := share(path, mustRecipients(t, id.Recipient().String()), shared, false); err != nil {
		t.Fatal(err)
	}
	withStdin(t, "theirpw\n")
	if err := importFile(shared, []string{idFile}, "", choiceCustom); err != nil {
		t.Fatal(err)
	}
	if got := decryptFile(t, filepath.Join(dir, "shared"), "theirpw"); got != "round trip\n" {
		t.Errorf("got %q", got)
	}
}

// Several prompts must be answerable from one pipe.
func TestPromptsShareOnePipe(t *testing.T) {
	withStdin(t, "first\nsecond\nthird")
	for _, want := range []string{"first", "second", "third"} {
		got, err := prompt("")
		if err != nil || string(got) != want {
			t.Fatalf("got %q, %v; want %q", got, err, want)
		}
	}
	if _, err := prompt(""); err == nil {
		t.Error("expected EOF")
	}
}
