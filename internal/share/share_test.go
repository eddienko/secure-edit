package share

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/armor"
)

func TestParseListTakesKeysAndFilesAndSkipsComments(t *testing.T) {
	a, _ := age.GenerateX25519Identity()
	b, _ := age.GenerateX25519Identity()
	file := filepath.Join(t.TempDir(), "keys")
	os.WriteFile(file, []byte("# team\n\n"+b.Recipient().String()+"\n"), 0o600)
	recs, err := ParseList("# me\n  " + a.Recipient().String() + "  \n\n" + file + "\n")
	if err != nil || len(recs) != 2 {
		t.Fatalf("got %d recipients, %v", len(recs), err)
	}
}

func TestParseListErrors(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	for text, want := range map[string]string{
		"":                  "at least one",
		"# only a comment":  "at least one",
		"nonsense":          "not a public key",
		"age1xyz":           "invalid recipient",
		id.String():         "private key",
		"/no/such/file/xyz": "not a readable file",
	} {
		_, err := ParseList(text)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", text, err, want)
		}
		if err != nil && strings.Contains(err.Error(), id.String()) {
			t.Errorf("the private key was echoed: %v", err)
		}
	}
}

func TestEncryptRoundTripsBinaryAndArmored(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	for _, armored := range []bool{false, true} {
		data, err := Encrypt([]byte("hello"), recips(id), armored)
		if err != nil {
			t.Fatal(err)
		}
		var r io.Reader = bytes.NewReader(data)
		if armored {
			if !bytes.HasPrefix(data, []byte("-----BEGIN AGE")) {
				t.Errorf("not armored: %q", data[:20])
			}
			r = armor.NewReader(r)
		}
		d, err := age.Decrypt(r, id)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := io.ReadAll(d); string(got) != "hello" {
			t.Errorf("armored=%v: %q", armored, got)
		}
	}
}

func recips(id *age.X25519Identity) []age.Recipient { return []age.Recipient{id.Recipient()} }
