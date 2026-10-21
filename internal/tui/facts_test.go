package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/eddienko/secure-edit/internal/keystore"
	"github.com/eddienko/secure-edit/internal/vault"
)

var factsNow = time.Date(2026, 10, 21, 16, 0, 0, 0, time.Local)

func factsClock() time.Time { return factsNow }

// touch sets a file's modification time to d before factsNow.
func touch(t *testing.T, path string, before time.Duration) time.Time {
	t.Helper()
	mt := factsNow.Add(-before)
	if err := os.Chtimes(path, mt, mt); err != nil {
		t.Fatal(err)
	}
	return mt
}

// waitFact waits for a line of the facts block: the label, space, and the value.
func (h *harness) waitFact(label, value string) {
	h.t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		for _, row := range strings.Split(h.screen(), "\n") {
			if i := strings.Index(row, "│ "+label+" "); i >= 0 || strings.Contains(row, " "+label+" ") {
				rest := row[strings.Index(row, label)+len(label):]
				if strings.HasPrefix(strings.TrimLeft(rest, " "), value) && strings.HasPrefix(rest, " ") {
					return
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("no line of the facts reads %q: %q:\n%s", label, value, h.screen())
}

// ---- the wording ----------------------------------------------------------

func TestHumanSize(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0 bytes", 1: "1 byte", 999: "999 bytes", 1023: "1023 bytes",
		1024: "1.0 KiB", 1536: "1.5 KiB", 1<<20 - 1: "1024.0 KiB", 1 << 20: "1.0 MiB",
		5<<20 + 512<<10: "5.5 MiB", 3 << 30: "3.0 GiB",
	} {
		if got := humanSize(n); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestAgo(t *testing.T) {
	now := factsNow
	for d, want := range map[time.Duration]string{
		-time.Hour:                "just now", // a modification time in the future
		0:                         "just now",
		44 * time.Second:          "just now",
		50 * time.Second:          "1 minute ago",
		5 * time.Minute:           "5 minutes ago",
		44 * time.Minute:          "44 minutes ago",
		50 * time.Minute:          "1 hour ago",
		3 * time.Hour:             "3 hours ago",
		35 * time.Hour:            "35 hours ago",
		40 * time.Hour:            "2 days ago",
		3 * 24 * time.Hour:        "3 days ago",
		59 * 24 * time.Hour:       "59 days ago",
		70 * 24 * time.Hour:       "2 months ago",
		400 * 24 * time.Hour:      "13 months ago",
		800 * 24 * time.Hour:      "2 years ago",
		10 * 365 * 24 * time.Hour: "10 years ago",
	} {
		if got := ago(now, now.Add(-d)); got != want {
			t.Errorf("ago(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestModifiedGivesBothTheAgeAndTheDate(t *testing.T) {
	mt := time.Date(2026, 10, 18, 15, 48, 0, 0, time.Local)
	if got := modified(factsNow, mt); got != "3 days ago (18 Oct 2026, 15:48)" {
		t.Errorf("got %q", got)
	}
}

func TestAgeRecipients(t *testing.T) {
	for name, c := range map[string]struct{ head, want string }{
		"passphrase": {"age-encryption.org/v1\n-> scrypt abc 18\nXYZ\n--- mac\n", "a passphrase"},
		"one key":    {"age-encryption.org/v1\n-> X25519 abc\nXYZ\n--- mac\n", "1 key"},
		"two keys":   {"age-encryption.org/v1\n-> X25519 a\nX\n-> ssh-ed25519 b c\nY\n--- mac\n", "2 keys"},
		"armored":    {"-----BEGIN AGE ENCRYPTED FILE-----\nYWdl\n", "ASCII-armored"},
		"nothing":    {"age-encryption.org/v1\n--- mac\n", "not recognised"},
		// A "-> " line after the header's end is the payload, not a recipient.
		"after the header": {"age-encryption.org/v1\n-> X25519 a\nX\n--- mac\n-> scrypt in the body\n", "1 key"},
	} {
		if got := ageRecipients([]byte(c.head)); !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

// ---- the block in the locked pane -----------------------------------------

func TestTheLockedPaneShowsTheFacts(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "work")
	os.Mkdir(sub, 0o700)
	text := strings.Repeat("0123456789", 123) // 1230 bytes: 1.2 KiB
	p := labelledFile(t, sub, "vpn.txt", "pw", text, vault.Meta{Title: "Office VPN", Description: "the office"})
	mt := touch(t, p, 3*24*time.Hour)
	seditFile(t, sub, "vpn.txt.bak", "pw", "old")
	touch(t, p+".bak", 2*24*time.Hour)

	h := newHarness(t, dir, Options{Now: factsClock, PasswordCheckDelay: 10 * time.Millisecond})
	h.selectFile("work/")
	h.key(tcell.KeyRight)
	h.selectFile("vpn.txt")
	h.waitFor("LOCKED")
	h.waitFact("Folder", "work/")
	h.waitFact("Text", "1.2 KiB")
	h.waitFact("Modified", "3 days ago ("+stamp(mt)+")")
	h.waitFact("Backup", "yes, from 2 days")
	h.waitFact("Format", "sedit v2 · Argon2id, 1 pass, 64.0 KiB")
	h.waitFor("weaker than the")
	h.waitFact("Password", "will ask")
	h.onlyTerminalColours("the facts block")
	if h.session() != nil {
		t.Error("looking at the facts unlocked the file")
	}
	h.notOnScreen(text[:20])
}

func TestTheTextSizeIsExactAndATopLevelFileIsInThisFolder(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "a.txt", "pw", strings.Repeat("x", 5000))
	seditFile(t, dir, "b.txt", "pw", "")
	h := newHarness(t, dir, Options{Now: factsClock, PasswordCheckDelay: 10 * time.Millisecond})
	h.selectFile("a.txt")
	h.waitFact("Text", "4.9 KiB") // 5000 bytes, not the size of the file on disk
	h.waitFact("Folder", "this folder")
	h.selectFile("b.txt")
	h.waitFact("Text", "0 bytes")
	h.waitFact("Backup", "none")
}

func TestAnUnreadableHeaderIsSaidSo(t *testing.T) {
	dir := t.TempDir()
	good, _ := vault.Encrypt([]byte("pw"), []byte("x"), fastKDF)
	bad := append([]byte(nil), good...)
	bad[11], bad[12], bad[13], bad[14] = 0xff, 0xff, 0xff, 0xff // absurd memory
	os.WriteFile(filepath.Join(dir, "odd.txt"), bad, 0o600)
	h := newHarness(t, dir, Options{Now: factsClock, PasswordCheckDelay: 10 * time.Millisecond})
	h.selectFile("odd.txt")
	h.waitFact("Format", "not a valid sedit header")
	h.waitFact("Text", "") // and still a size: the file's
}

func TestAgeAndOtherFilesHaveTheirOwnFacts(t *testing.T) {
	dir := t.TempDir()
	plainFile(t, dir, "pass.age", "age-encryption.org/v1\n-> scrypt abc 18\nXYZ\n--- mac\nbody")
	plainFile(t, dir, "key.age", "age-encryption.org/v1\n-> X25519 abc\nXYZ\n--- mac\nbody")
	plainFile(t, dir, "notes.md", "plain text")
	h := newHarness(t, dir, Options{Now: factsClock, PasswordCheckDelay: 10 * time.Millisecond})
	h.selectFile("pass.age")
	h.waitFact("Opens with", "a passphrase")
	h.waitFor("To turn it into a sedit file") // the existing message is still there
	h.selectFile("key.age")
	h.waitFact("Opens with", "1 key")
	h.selectFile("notes.md")
	h.waitFact("Size", "10 bytes")
	h.waitFor("Not a sedit file")
	h.notOnScreen("Password")
	h.notOnScreen("Format")
}

func TestAFolderHasNoFactsBlock(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "sub"), 0o700)
	h := newHarness(t, dir, Options{Now: factsClock})
	h.selectFile("sub/")
	h.waitFor("Empty folder.")
	h.notOnScreen("Modified")
}

func TestAnOpenFileShowsNoFacts(t *testing.T) {
	dir := t.TempDir()
	p := seditFile(t, dir, "a.txt", "pw", secretText)
	h := newHarness(t, dir, Options{Now: factsClock, PasswordCheckDelay: 10 * time.Millisecond})
	h.remember(p)
	h.selectFile("a.txt")
	h.waitFor("Modified")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	h.waitGone("Modified")
	h.notOnScreen("Format")
}

func TestTheFactsMakeWayInASmallPane(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "a.txt", "pw", "x")
	h := newHarness(t, dir, Options{Now: factsClock, PasswordCheckDelay: 10 * time.Millisecond, TickEvery: 50 * time.Millisecond})
	h.selectFile("a.txt")
	h.waitFor("Format")
	// Too short for the block: the message stays whole and nothing is cut in half.
	h.resize(110, 14)
	h.waitFor("LOCKED")
	scr := h.screen()
	for _, label := range []string{"Folder", "Text", "Modified", "Backup", "Password", "Format"} {
		if i := strings.Index(scr, label+" "); i >= 0 {
			// Whatever is drawn is a whole line: a label is followed by its value.
			rest := scr[i+len(label):]
			if nl := strings.Index(rest, "\n"); strings.TrimSpace(rest[:nl][:min(nl, 8)]) == "" && !strings.Contains(rest[:nl], "│") {
				t.Errorf("%s has no value:\n%s", label, scr)
			}
		}
	}
	h.resize(40, 30) // narrow: values wrap, nothing runs off the pane
	h.waitFor("LOCKED")
	h.settle()
	for _, row := range strings.Split(h.screen(), "\n") {
		if len([]rune(row)) > 40 {
			t.Errorf("a row is wider than the screen: %q", row)
		}
	}
}

// ---- the Password line ----------------------------------------------------

func TestThePasswordLineSaysHowTheFileWillUnlock(t *testing.T) {
	dir := t.TempDir()
	stored := seditFile(t, dir, "stored.txt", "pw", "x")
	seditFile(t, dir, "other.txt", "pw", "x")
	h := newHarness(t, dir, Options{Now: factsClock, PasswordCheckDelay: 10 * time.Millisecond})
	h.remember(stored)

	h.selectFile("stored.txt")
	h.waitFact("Password", "stored for this file")
	h.selectFile("other.txt")
	h.waitFact("Password", "will ask")

	h.store.put(keystore.DefaultAccount, "dpw") // and now a default exists
	h.press('R')                                // the answers are asked again after a reload
	h.waitFact("Password", "the default password, if it fits;")
	h.selectFile("stored.txt")
	h.waitFact("Password", "stored for this file") // the file's own wins over the default
}

func TestThePasswordLineNeverReadsAPassword(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		paths = append(paths, seditFile(t, dir, n, "pw", "x"))
	}
	h := newHarness(t, dir, Options{Now: factsClock, PasswordCheckDelay: 10 * time.Millisecond})
	h.remember(paths[1])
	h.defaultPassword("dpw")
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		h.selectFile(n)
		h.waitFact("Password", "")
		h.waitGone("checking the Keychain")
	}
	asked, gets := h.store.asked()
	if gets != 0 {
		t.Errorf("%d passwords were read just to describe the files", gets)
	}
	if len(asked) == 0 {
		t.Error("the Keychain was never asked")
	}
	if h.session() != nil {
		t.Error("a file was unlocked")
	}
}

func (h *harness) defaultPassword(pw string) { h.store.put(keystore.DefaultAccount, pw) }

func TestTheKeychainIsNotAskedForFilesTheCursorPassesOver(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"} {
		seditFile(t, dir, n, "pw", "x")
	}
	h := newHarness(t, dir, Options{Now: factsClock, PasswordCheckDelay: 300 * time.Millisecond})
	h.waitFor("a.txt")
	h.key(tcell.KeyHome)
	for i := 0; i < 5; i++ { // straight down the list, faster than the delay
		h.key(tcell.KeyDown)
	}
	h.waitFor("checking the Keychain") // the file it came to rest on
	h.waitFact("Password", "will ask")
	asked, _ := h.store.asked()
	files := 0
	for _, a := range asked {
		if strings.HasSuffix(a, ".txt") {
			files++
			if !strings.HasSuffix(a, "e.txt") {
				t.Errorf("the Keychain was asked about %s, which the cursor only passed", a)
			}
		}
	}
	if files != 1 {
		t.Errorf("asked about %d files, want 1: %v", files, asked)
	}
}

func TestTheAnswerIsRememberedUntilTheFolderIsReloaded(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "a.txt", "pw", "x")
	seditFile(t, dir, "b.txt", "pw", "x")
	h := newHarness(t, dir, Options{Now: factsClock, PasswordCheckDelay: 10 * time.Millisecond})
	count := func(suffix string) (n int) {
		asked, _ := h.store.asked()
		for _, a := range asked {
			if strings.HasSuffix(a, suffix) {
				n++
			}
		}
		return n
	}
	h.selectFile("a.txt")
	h.waitFact("Password", "will ask")
	h.selectFile("b.txt")
	h.waitFact("Password", "will ask")
	h.selectFile("a.txt") // back again: no new question
	h.waitFact("Password", "will ask")
	h.settle()
	if n := count("a.txt"); n != 1 {
		t.Errorf("a.txt was asked about %d times, want 1", n)
	}
	h.press('R')
	h.waitFact("Password", "will ask")
	h.settle()
	time.Sleep(100 * time.Millisecond)
	if n := count("a.txt"); n != 2 {
		t.Errorf("after a reload a.txt was asked about %d times, want 2", n)
	}
}

func TestTheChecksResultDoesNotCoverAnUnlockedText(t *testing.T) {
	dir := t.TempDir()
	p := seditFile(t, dir, "a.txt", "pw", secretText)
	h := newHarness(t, dir, Options{Now: factsClock, PasswordCheckDelay: 150 * time.Millisecond})
	h.remember(p)
	h.selectFile("a.txt")
	h.key(tcell.KeyEnter) // before the check has come back
	h.waitFor(secretText)
	time.Sleep(400 * time.Millisecond) // it comes back now
	h.settle()
	h.waitFor(secretText)
	h.notOnScreen("Modified")
}

func TestWithoutAKeychainTheFileWillAsk(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "a.txt", "pw", "x")
	h := newHarness(t, dir, Options{Now: factsClock, PasswordCheckDelay: 10 * time.Millisecond})
	old := keystore.Backend
	keystore.Backend = nil
	defer func() { keystore.Backend = old }()
	h.selectFile("a.txt")
	h.waitFact("Password", "will ask")
	if asked, _ := h.store.asked(); len(asked) != 0 {
		t.Errorf("asked %v though there is no Keychain", asked)
	}
}
