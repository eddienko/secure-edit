package tui

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"filippo.io/age/armor"
	"github.com/gdamore/tcell/v2"
)

// exportHarness opens a folder with one file and a new age identity to export to.
func exportHarness(t *testing.T, clipb *fakeClip) (h *harness, dir string, id *age.X25519Identity) {
	t.Helper()
	dir = t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", secretText+"\n")
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	h = newHarness(t, dir, Options{Clipboard: clipb, ClipboardTTL: time.Hour})
	h.remember(path)
	h.selectFile("notes.txt")
	return h, dir, id
}

func (h *harness) askExport(recipients string) {
	h.press('x')
	h.waitFor("Recipients")
	h.typeText(recipients)
	h.key(tcell.KeyCtrlS)
}

func decryptAge(t *testing.T, data []byte, id age.Identity, armored bool) string {
	t.Helper()
	var r io.Reader = bytes.NewReader(data)
	if armored {
		r = armor.NewReader(r)
	}
	d, err := age.Decrypt(r, id)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(d)
	return string(out)
}

func TestExportWritesACopyTheRecipientCanOpen(t *testing.T) {
	h, dir, id := exportHarness(t, &fakeClip{})
	h.askExport(id.Recipient().String())
	h.waitFor("to 1 recipient?")
	h.key(tcell.KeyEnter) // Write a file
	h.waitFor("exported to exported/notes.txt.age")

	out := filepath.Join(dir, "exported", "notes.txt.age")
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := decryptAge(t, data, id, false); got != secretText+"\n" {
		t.Errorf("the copy holds %q", got)
	}
	if fi, _ := os.Stat(out); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	if bytes.Contains(data, []byte(secretText)) {
		t.Error("the copy holds the text in the clear")
	}
	if s := h.session(); s != nil && !s.Locked() {
		t.Error("the file was left unlocked")
	}

	// Exporting again never overwrites the earlier copy.
	h.selectFile("notes.txt")
	h.askExport(id.Recipient().String())
	h.waitFor("to 1 recipient?")
	h.key(tcell.KeyEnter)
	h.waitFor("exported/notes.txt.2.age")
	if _, err := os.Stat(out); err != nil {
		t.Error("the first copy went away")
	}
}

func TestExportAsTextPutsArmoredTextOnTheClipboard(t *testing.T) {
	clipb := &fakeClip{}
	h, dir, id := exportHarness(t, clipb)
	h.askExport(id.Recipient().String())
	h.waitFor("recipient?")
	h.key(tcell.KeyTab) // Copy as text
	h.key(tcell.KeyEnter)
	h.waitFor("encrypted, as text")
	got := clipb.text()
	if !strings.HasPrefix(got, "-----BEGIN AGE ENCRYPTED FILE-----") {
		t.Fatalf("clipboard holds %q", got)
	}
	if text := decryptAge(t, []byte(got), id, true); text != secretText+"\n" {
		t.Errorf("the text holds %q", text)
	}
	if strings.Contains(h.status(), "launch codes") {
		t.Error("the status line shows the secret")
	}
	if _, err := os.Stat(filepath.Join(dir, "exported")); err == nil {
		t.Error("a file was written as well")
	}
}

func TestExportTakesAFileOfKeysAndSeveralRecipients(t *testing.T) {
	h, dir, id := exportHarness(t, &fakeClip{})
	other, _ := age.GenerateX25519Identity()
	keys := filepath.Join(dir, "keys.txt")
	os.WriteFile(keys, []byte("# bob\n"+other.Recipient().String()+"\n"), 0o600)
	h.press('x')
	h.waitFor("Recipients")
	h.typeText(id.Recipient().String())
	h.key(tcell.KeyEnter) // a new line in the box
	h.typeText(keys)
	h.key(tcell.KeyCtrlS)
	h.waitFor("to 2 recipients?")
	h.key(tcell.KeyEnter)
	h.waitFor("exported to")
	data, _ := os.ReadFile(filepath.Join(dir, "exported", "notes.txt.age"))
	for _, who := range []*age.X25519Identity{id, other} {
		if got := decryptAge(t, data, who, false); got != secretText+"\n" {
			t.Errorf("a recipient got %q", got)
		}
	}
}

func TestExportRefusesBadRecipientsAndKeepsTheBoxOpen(t *testing.T) {
	h, dir, _ := exportHarness(t, &fakeClip{})
	h.askExport("not a key")
	h.waitFor("not a public key")
	h.waitFor("Recipients") // still open

	// A private key is refused without being shown.
	h.key(tcell.KeyCtrlA)
	for i := 0; i < len("not a key"); i++ {
		h.key(tcell.KeyDelete)
	}
	secret, _ := age.GenerateX25519Identity()
	h.typeText(secret.String())
	h.key(tcell.KeyCtrlS)
	h.waitFor("that is a private key")
	if strings.Contains(h.status(), secret.String()[:30]) {
		t.Error("the private key was echoed")
	}
	h.key(tcell.KeyEscape)
	h.waitGone("Recipients")
	if _, err := os.Stat(filepath.Join(dir, "exported")); err == nil {
		t.Error("something was exported")
	}
}

func TestExportOnAFolderSaysWhatToSelect(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "sub"), 0o700)
	h := newHarness(t, dir, Options{Clipboard: &fakeClip{}})
	h.selectFile("sub")
	h.press('x')
	h.waitFor("select a sedit file to export")
}
