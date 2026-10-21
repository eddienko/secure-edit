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

// harness runs a real App on a simulated screen.
type harness struct {
	t     *testing.T
	a     *App
	sim   tcell.SimulationScreen
	done  chan error
	store *fakeStore
}

func newHarness(t *testing.T, root string, opts Options) *harness {
	t.Helper()
	store := useStore(t) // the real Keychain is never touched
	t.Setenv("SEDIT_STATUSLINE", "0")
	sim := tcell.NewSimulationScreen("UTF-8")
	opts.Screen = sim
	a, err := New(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	sim.SetSize(110, 32)
	h := &harness{t: t, a: a, sim: sim, done: make(chan error, 1), store: store}
	go func() { h.done <- a.Run() }()
	t.Cleanup(func() {
		a.app.Stop()
		select {
		case <-h.done:
		case <-time.After(3 * time.Second):
		}
	})
	h.waitFor(filepath.Base(root))
	return h
}

// screen returns what is drawn, one string per row.
//
// It reads on the UI goroutine: tcell's simulated screen hands out its live cell
// buffer, so reading it while the UI draws would be a data race.
func (h *harness) screen() string {
	var out string
	h.do(func() {
		cells, w, ht := h.sim.GetContents()
		var rows []string
		for y := 0; y < ht; y++ {
			var b strings.Builder
			for x := 0; x < w; x++ {
				if c := cells[y*w+x]; len(c.Runes) > 0 {
					b.WriteRune(c.Runes[0])
				} else {
					b.WriteByte(' ')
				}
			}
			rows = append(rows, strings.TrimRight(b.String(), " "))
		}
		out = strings.Join(rows, "\n")
	})
	return out
}

func (h *harness) waitFor(want string) {
	h.t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(h.screen(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("%q never appeared on screen:\n%s", want, h.screen())
}

func (h *harness) waitGone(unwanted string) {
	h.t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if !strings.Contains(h.screen(), unwanted) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("%q is still on screen:\n%s", unwanted, h.screen())
}

// settle gives the UI time to process what was injected, for checks that
// something did NOT happen.
func (h *harness) settle() {
	h.t.Helper()
	h.do(func() {})
	time.Sleep(60 * time.Millisecond)
	h.do(func() {})
}

func (h *harness) notOnScreen(unwanted string) {
	h.t.Helper()
	h.settle()
	if strings.Contains(h.screen(), unwanted) {
		h.t.Fatalf("%q is on screen:\n%s", unwanted, h.screen())
	}
}

// do runs f on the UI goroutine and waits for it.
func (h *harness) do(f func()) {
	h.t.Helper()
	done := make(chan struct{})
	h.a.app.QueueUpdate(func() { f(); close(done) })
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		h.t.Fatal("the UI did not respond")
	}
}

func (h *harness) key(k tcell.Key) { h.sim.InjectKey(k, 0, tcell.ModNone) }

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.sim.InjectKey(tcell.KeyRune, r, tcell.ModNone)
	}
}

// press types one character as a command key.
func (h *harness) press(r rune) { h.sim.InjectKey(tcell.KeyRune, r, tcell.ModNone) }

// selectFile moves the selection to the entry with this name.
func (h *harness) selectFile(name string) {
	h.t.Helper()
	h.waitFor(name)
	name = strings.TrimSuffix(name, "/") // a folder is drawn with a slash, named without
	h.key(tcell.KeyHome)                 // start from the top, so the order of entries doesn't matter
	h.settle()
	for i := 0; i < 50; i++ {
		var cur string
		h.do(func() {
			if e, ok := h.a.current(); ok {
				cur = e.Name
			}
		})
		if cur == name {
			return
		}
		h.key(tcell.KeyDown)
		h.settle()
	}
	h.t.Fatalf("could not select %q:\n%s", name, h.screen())
}

func (h *harness) session() (s *Session) {
	h.do(func() { s = h.a.session })
	return s
}

func (h *harness) status() string {
	var s string
	h.do(func() { s = h.a.msg })
	return s
}

// ---- the tests ----------------------------------------------------------

const secretText = "the launch codes are 12345"

func TestShowsFilesWithBadgesAndHidesBookkeeping(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "notes.txt", "pw", secretText)
	seditFile(t, dir, "notes.txt.bak", "pw", "old")
	plainFile(t, dir, "readme.md", "plain")
	plainFile(t, dir, "shared.age", "age-encryption.org/v1\n")
	plainFile(t, dir, ".notes.txt.lock", "")
	os.Mkdir(filepath.Join(dir, "projects"), 0o700)

	h := newHarness(t, dir, Options{})
	h.waitFor("▪ notes.txt")
	for _, want := range []string{"projects/", "○ shared.age", "· readme.md"} {
		h.waitFor(want)
	}
	h.notOnScreen("notes.txt.bak")
	h.notOnScreen(".notes.txt.lock")
}

func TestAFileIsLockedUntilYouPressEnter(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "notes.txt", "pw", secretText)
	h := newHarness(t, dir, Options{})

	h.selectFile("notes.txt")
	h.waitFor("LOCKED")
	h.waitFor("Press Enter to unlock")
	h.notOnScreen(secretText) // not even after moving the cursor over it
	if h.session() != nil {
		t.Error("a file was opened without being asked")
	}
}

func TestEnterUnlocksWithAStoredPassword(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", secretText)
	h := newHarness(t, dir, Options{})
	h.store.m[account(t, path)] = []byte("pw")

	h.selectFile("notes.txt")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	h.waitFor("unlocked, locks in")
	h.notOnScreen("LOCKED")
}

func TestEnterAsksForThePasswordAndNeverShowsIt(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "notes.txt", "hunter2", secretText)
	h := newHarness(t, dir, Options{})
	h.selectFile("notes.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("Password:")

	// A wrong password: told so, asked again, and the dots are all that show.
	h.typeText("wrongpass")
	h.waitFor("•••••••••")
	h.notOnScreen("wrongpass")
	h.key(tcell.KeyEnter)
	h.waitFor("Wrong password, try again")
	h.notOnScreen(secretText)

	h.typeText("hunter2")
	h.waitFor("•••••••")
	h.notOnScreen("hunter2")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	h.notOnScreen("hunter2")
	h.notOnScreen("Password:")
}

func TestEscapeCancelsThePasswordDialog(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "notes.txt", "pw", secretText)
	h := newHarness(t, dir, Options{})
	h.selectFile("notes.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("Password:")
	h.typeText("pw")
	h.key(tcell.KeyEscape)
	h.waitGone("Password:")
	h.waitFor("LOCKED")
	h.notOnScreen(secretText)
}

func TestMovingToAnotherFileLocksTheOneYouWereReading(t *testing.T) {
	dir := t.TempDir()
	a := seditFile(t, dir, "a.txt", "pw", secretText)
	seditFile(t, dir, "b.txt", "pw", "second file")
	h := newHarness(t, dir, Options{})
	h.store.m[account(t, a)] = []byte("pw")

	h.selectFile("a.txt")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	s := h.session()
	pw, plain := s.Password, s.Plain // the memory behind the session, to look at afterwards

	h.key(tcell.KeyDown)
	h.waitFor("LOCKED")
	h.notOnScreen(secretText)
	if h.session() != nil {
		t.Error("the session was kept")
	}
	for _, b := range append(append([]byte{}, pw...), plain...) {
		if b != 0 {
			t.Fatal("the unlocked text or password was not zeroed")
		}
	}
}

func TestTheIdleTimerLocksTheFile(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", secretText)
	h := newHarness(t, dir, Options{Idle: time.Second, TickEvery: 50 * time.Millisecond})
	h.store.m[account(t, path)] = []byte("pw")

	h.selectFile("notes.txt")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	// No key presses now: it locks by itself.
	h.waitFor("LOCKED")
	h.waitFor("was locked after")
	h.notOnScreen(secretText)
	if h.session() != nil {
		t.Error("the session was kept")
	}
}

func TestKeyPressesKeepTheFileUnlocked(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", secretText)
	h := newHarness(t, dir, Options{Idle: time.Second, TickEvery: 50 * time.Millisecond})
	h.store.m[account(t, path)] = []byte("pw")
	h.selectFile("notes.txt")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	for i := 0; i < 14; i++ { // about 3.5 s: well past the idle time
		time.Sleep(250 * time.Millisecond)
		h.key(tcell.KeyTab) // any key counts as activity
		h.key(tcell.KeyTab)
	}
	h.waitFor(secretText)
}

func TestLockKey(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", secretText)
	h := newHarness(t, dir, Options{})
	h.store.m[account(t, path)] = []byte("pw")
	h.selectFile("notes.txt")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	h.press('l')
	h.waitFor("LOCKED")
	h.notOnScreen(secretText)
}

func TestQuitWipesAndStops(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", secretText)
	h := newHarness(t, dir, Options{})
	h.store.m[account(t, path)] = []byte("pw")
	h.selectFile("notes.txt")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	s := h.session()
	plain := s.Plain
	h.press('q')
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the app did not stop")
	}
	for _, b := range plain {
		if b != 0 {
			t.Fatal("the text was not zeroed on quit")
		}
	}
}

func TestEditingSavesAndShowsTheResult(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", "original\n")
	h := newHarness(t, dir, Options{})
	h.store.m[account(t, path)] = []byte("pw")
	useEditor(t, `echo "added by the editor" >> "$1"`+"\n")

	h.selectFile("notes.txt")
	h.press('e') // unlocks (stored password) and edits
	h.waitFor("saved notes.txt")
	h.waitFor("added by the editor")
	if got := decrypt(t, path, "pw"); got != "original\nadded by the editor\n" {
		t.Errorf("file holds %q", got)
	}
	if got := decrypt(t, vault.BackupPath(path), "pw"); got != "original\n" {
		t.Errorf("backup holds %q", got)
	}
}

func TestEditingAsksForThePasswordFirst(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", "original\n")
	h := newHarness(t, dir, Options{})
	useEditor(t, `echo more >> "$1"`+"\n")
	h.selectFile("notes.txt")
	h.press('e')
	h.waitFor("Password:")
	h.typeText("pw")
	h.key(tcell.KeyEnter)
	h.waitFor("saved notes.txt")
	if got := decrypt(t, path, "pw"); got != "original\nmore\n" {
		t.Errorf("file holds %q", got)
	}
}

func TestEditingWithoutChangesSaysSo(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", "same\n")
	h := newHarness(t, dir, Options{})
	h.store.m[account(t, path)] = []byte("pw")
	useEditor(t, "true\n")
	h.selectFile("notes.txt")
	h.press('e')
	h.waitFor("no changes to notes.txt")
	if _, err := os.Stat(vault.BackupPath(path)); err == nil {
		t.Error("a backup was made")
	}
}

func TestNewFile(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, dir, Options{})
	useEditor(t, `echo "brand new text" >> "$1"`+"\n")

	h.press('n')
	h.waitFor("Name:")
	h.typeText("fresh.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("Choose a password:")
	h.typeText("s3cret")
	h.key(tcell.KeyEnter)
	h.waitFor("Type it again:")
	h.typeText("s3cret")
	h.notOnScreen("s3cret")
	h.key(tcell.KeyEnter)

	h.waitFor("saved fresh.txt")
	h.waitFor("▪ fresh.txt")
	h.waitFor("brand new text")
	if got := decrypt(t, filepath.Join(dir, "fresh.txt"), "s3cret"); got != "brand new text\n" {
		t.Errorf("file holds %q", got)
	}
}

func TestNewFilePasswordsMustMatch(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, dir, Options{})
	useEditor(t, `echo x >> "$1"`+"\n")
	h.press('n')
	h.waitFor("Name:")
	h.typeText("fresh.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("Choose a password:")
	h.typeText("one")
	h.key(tcell.KeyEnter)
	h.waitFor("Type it again:")
	h.typeText("two")
	h.key(tcell.KeyEnter)
	h.waitFor("The passwords didn't match, try again")
	h.notOnScreen("saved fresh.txt")
	if _, err := os.Stat(filepath.Join(dir, "fresh.txt")); err == nil {
		t.Error("the file was created")
	}
}

func TestNewFileLeftEmptyIsNotCreated(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, dir, Options{})
	useEditor(t, "true\n")
	h.press('n')
	h.waitFor("Name:")
	h.typeText("nothing.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("Choose a password:")
	h.typeText("pw")
	h.key(tcell.KeyEnter)
	h.waitFor("Type it again:")
	h.typeText("pw")
	h.key(tcell.KeyEnter)
	h.waitFor("no changes to nothing.txt")
	h.notOnScreen("▪ nothing.txt")
	es, _ := os.ReadDir(dir)
	if len(es) != 0 {
		t.Errorf("the directory holds %d entries, want none", len(es))
	}
}

func TestNewFileRefusesBadNames(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "taken.txt", "pw", "x")
	h := newHarness(t, dir, Options{})
	for name, want := range map[string]string{
		"sub/dir.txt": "not a path",
		".hidden":     "hidden from the list",
		"old.bak":     "hidden from the list",
		"taken.txt":   "already exists",
	} {
		h.press('n')
		h.waitFor("Name:")
		h.typeText(name)
		h.key(tcell.KeyEnter)
		h.waitFor(want)
		h.notOnScreen("Choose a password:")
	}
}

func TestNewFileOffersTheDefaultPassword(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, dir, Options{})
	h.store.m[keystore.DefaultAccount] = []byte("dpw")
	useEditor(t, `echo "with the default" >> "$1"`+"\n")

	h.press('n')
	h.waitFor("Name:")
	h.typeText("d.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("Which password should be used?")
	h.key(tcell.KeyTab) // from "Custom password" to "Default password"
	h.key(tcell.KeyEnter)
	h.waitFor("saved d.txt")
	if got := decrypt(t, filepath.Join(dir, "d.txt"), "dpw"); got != "with the default\n" {
		t.Errorf("file holds %q", got)
	}
}

func TestRename(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "old.txt", "pw", secretText)
	seditFile(t, dir, "old.txt.bak", "pw", "previous")
	h := newHarness(t, dir, Options{})
	h.store.m[account(t, path)] = []byte("pw")

	h.selectFile("old.txt")
	h.press('r')
	h.waitFor("New name or path:")
	for i := 0; i < len("old.txt"); i++ {
		h.key(tcell.KeyBackspace2)
	}
	h.typeText("new.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("▪ new.txt")
	h.waitFor("its backup and its stored password")
	h.notOnScreen("▪ old.txt")
	newPath := filepath.Join(dir, "new.txt")
	if got := decrypt(t, newPath, "pw"); got != secretText {
		t.Errorf("file holds %q", got)
	}
	decrypt(t, vault.BackupPath(newPath), "pw")
	if string(h.store.m[account(t, newPath)]) != "pw" {
		t.Error("the stored password did not follow the file")
	}
	if _, ok := h.store.m[account(t, path)]; ok {
		t.Error("the old entry is still there")
	}
	for _, p := range []string{path, vault.BackupPath(path)} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s is still there", filepath.Base(p))
		}
	}
}

func TestRenameRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "a.txt", "pw", "a")
	seditFile(t, dir, "b.txt", "pw", "b")
	h := newHarness(t, dir, Options{})
	h.selectFile("a.txt")
	h.press('r')
	h.waitFor("New name or path:")
	for i := 0; i < len("a.txt"); i++ {
		h.key(tcell.KeyBackspace2)
	}
	h.typeText("b.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("refusing to overwrite")
	if got := decrypt(t, filepath.Join(dir, "b.txt"), "pw"); got != "b" {
		t.Errorf("b.txt holds %q", got)
	}
}

func TestDeleteAsksFirstAndCancelIsTheDefault(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "doomed.txt", "pw", secretText)
	seditFile(t, dir, "doomed.txt.bak", "pw", "previous")
	h := newHarness(t, dir, Options{})
	h.store.m[account(t, path)] = []byte("pw")

	h.selectFile("doomed.txt")
	h.press('d')
	h.waitFor("Delete doomed.txt?")
	h.waitFor("not a secure erase")
	h.key(tcell.KeyEnter) // the focused button is Cancel
	h.waitGone("Delete doomed.txt?")
	decrypt(t, path, "pw")

	h.press('d')
	h.waitFor("Delete doomed.txt?")
	h.key(tcell.KeyEscape)
	h.waitGone("Delete doomed.txt?")
	decrypt(t, path, "pw")

	h.press('d')
	h.waitFor("Delete doomed.txt?")
	h.key(tcell.KeyTab) // to "Delete"
	h.key(tcell.KeyEnter)
	h.waitFor("deleted doomed.txt")
	h.notOnScreen("▪ doomed.txt")
	for _, p := range []string{path, vault.BackupPath(path)} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s is still there", filepath.Base(p))
		}
	}
	if len(h.store.m) != 0 {
		t.Errorf("entries left: %v", h.store.m)
	}
}

func TestChangePassword(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "old", secretText)
	h := newHarness(t, dir, Options{})
	h.selectFile("notes.txt")
	h.press('p')
	h.waitFor("Password:") // the current one
	h.typeText("old")
	h.key(tcell.KeyEnter)
	h.waitFor("Choose a password:")
	h.typeText("brandnew")
	h.key(tcell.KeyEnter)
	h.waitFor("Type it again:")
	h.typeText("brandnew")
	h.key(tcell.KeyEnter)
	h.waitFor("password changed for notes.txt")
	if got := decrypt(t, path, "brandnew"); got != secretText {
		t.Errorf("file holds %q", got)
	}
	if _, err := vault.Decrypt([]byte("old"), readFile(t, path)); err == nil {
		t.Error("the old password still works")
	}
}

func TestFoldersOpenAndClose(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "projects")
	os.Mkdir(sub, 0o700)
	seditFile(t, sub, "inner.txt", "pw", "x")
	h := newHarness(t, dir, Options{})

	h.selectFile("projects/")
	h.notOnScreen("inner.txt")
	h.key(tcell.KeyRight)
	h.waitFor("▪ inner.txt")
	h.key(tcell.KeyLeft)
	h.waitGone("inner.txt")
	h.key(tcell.KeyEnter) // Enter toggles too
	h.waitFor("▪ inner.txt")
	h.key(tcell.KeyDown)
	h.key(tcell.KeyLeft) // on a file, Left goes to its folder
	h.settle()
	h.do(func() {
		if e, _ := h.a.current(); e.Name != "projects" {
			t.Errorf("selected %q, want the folder", e.Name)
		}
	})
}

func TestRefreshPicksUpNewFiles(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, dir, Options{})
	seditFile(t, dir, "late.txt", "pw", "x")
	h.notOnScreen("late.txt")
	h.press('R')
	h.waitFor("▪ late.txt")
}

func TestHelp(t *testing.T) {
	h := newHarness(t, t.TempDir(), Options{})
	h.press('?')
	h.waitFor("Nothing is decrypted until you press Enter")
	h.key(tcell.KeyEnter)
	h.waitGone("Nothing is decrypted until you press Enter")
}

func TestNonSeditFilesGetAHintNotAnUnlock(t *testing.T) {
	dir := t.TempDir()
	plainFile(t, dir, "readme.md", "plain text")
	plainFile(t, dir, "shared.age", "age-encryption.org/v1\n")
	h := newHarness(t, dir, Options{})
	h.selectFile("shared.age")
	h.waitFor("sedit --import shared.age")
	h.key(tcell.KeyEnter)
	h.notOnScreen("Password:")
	h.selectFile("readme.md")
	h.waitFor("Not a sedit file")
	h.waitFor("sedit --encrypt readme.md")
	h.press('e')
	h.waitFor("select a sedit file first")
	h.notOnScreen("Password:")
}

func TestTheShownTextScrollsAndWraps(t *testing.T) {
	dir := t.TempDir()
	var long strings.Builder
	for i := 1; i <= 80; i++ {
		long.WriteString("line number " + strings.Repeat("x", i%7) + "\n")
	}
	long.WriteString("THE VERY LAST LINE\n")
	path := seditFile(t, dir, "long.txt", "pw", long.String())
	h := newHarness(t, dir, Options{})
	h.store.m[account(t, path)] = []byte("pw")
	h.selectFile("long.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("line number")
	h.notOnScreen("THE VERY LAST LINE")

	h.key(tcell.KeyTab) // focus the preview
	h.key(tcell.KeyEnd)
	h.waitFor("THE VERY LAST LINE")
	h.key(tcell.KeyHome)
	h.waitGone("THE VERY LAST LINE")
	h.key(tcell.KeyPgDn)
	h.settle()
	h.key(tcell.KeyEscape) // back to the list
	h.key(tcell.KeyDown)   // moves in the list again (and locks, as it moves on)
	h.settle()
}

func TestBinaryContentIsNotDumpedOnScreen(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "blob.bin", "pw", "abc\x00\x01\x02def")
	h := newHarness(t, dir, Options{})
	h.store.m[account(t, path)] = []byte("pw")
	h.selectFile("blob.bin")
	h.key(tcell.KeyEnter)
	h.waitFor("Binary data (9 bytes): not shown.")
	h.notOnScreen("abc")
}

func TestUnlockingAFileThatChangedKindIsReported(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", "x")
	h := newHarness(t, dir, Options{})
	h.selectFile("notes.txt")
	// The file turns into plain text behind the app's back.
	os.WriteFile(path, []byte("now plain"), 0o600)
	h.key(tcell.KeyEnter)
	h.waitFor("not a sedit file")
	h.notOnScreen("Password:")
}

func TestHomeAndEndMoveTheSelection(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "afolder"), 0o700)
	plainFile(t, dir, "bfile.md", "x")
	plainFile(t, dir, "zfile.md", "x")
	h := newHarness(t, dir, Options{})
	at := func() (name string) {
		h.settle()
		h.do(func() { e, _ := h.a.current(); name = e.Name })
		return
	}
	h.key(tcell.KeyEnd)
	if got := at(); got != "zfile.md" {
		t.Errorf("End: %q", got)
	}
	h.key(tcell.KeyHome)
	if got := at(); got != filepath.Base(dir) {
		t.Errorf("Home: %q, want the folder itself", got)
	}
	h.press('G')
	if got := at(); got != "zfile.md" {
		t.Errorf("G: %q", got)
	}
	h.press('g')
	if got := at(); got != filepath.Base(dir) {
		t.Errorf("g: %q", got)
	}
}

func TestTreeTitleIsShort(t *testing.T) {
	home, _ := os.UserHomeDir()
	for in, want := range map[string]string{
		filepath.Join(home, "secrets"): filepath.Join("~", "secrets"),
		home:                           home,
		"/etc":                         "/etc",
		"/a/very/long/path/with/many/parts/inside/project":    "…/project",
		filepath.Join(home, "a/very/long/path/to/some/where"): "…/where",
	} {
		if got := treeTitle(in); got != want {
			t.Errorf("treeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- colours ------------------------------------------------------------

type cellInfo struct {
	r      rune
	fg, bg tcell.Color
	attr   tcell.AttrMask
}

// grid reads every cell of the screen, with its colours and attributes.
func (h *harness) grid() (g [][]cellInfo) {
	h.do(func() {
		cells, w, ht := h.sim.GetContents()
		for y := 0; y < ht; y++ {
			row := make([]cellInfo, w)
			for x := 0; x < w; x++ {
				c := cells[y*w+x]
				fg, bg, attr := c.Style.Decompose()
				r := ' '
				if len(c.Runes) > 0 {
					r = c.Runes[0]
				}
				row[x] = cellInfo{r, fg, bg, attr}
			}
			g = append(g, row)
		}
	})
	return g
}

// onlyTerminalColours fails if any cell on screen has a colour of its own. The
// interface must look right in every terminal theme, so it may only use the
// terminal's default foreground and background.
func (h *harness) onlyTerminalColours(state string) {
	h.t.Helper()
	h.settle()
	for y, row := range h.grid() {
		for x, c := range row {
			if c.fg != tcell.ColorDefault || c.bg != tcell.ColorDefault {
				h.t.Fatalf("%s: the cell at column %d, row %d (%q) has colours fg=%v bg=%v; only the terminal's defaults are allowed\n%s",
					state, x, y, c.r, c.fg, c.bg, h.screen())
			}
		}
	}
}

func TestUsesOnlyTheTerminalsOwnColours(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", secretText)
	blob := seditFile(t, dir, "blob.bin", "pw", "abc\x00\x01def")
	plainFile(t, dir, "readme.md", "plain text")
	plainFile(t, dir, "shared.age", "age-encryption.org/v1\n")
	os.Mkdir(filepath.Join(dir, "projects"), 0o700)
	h := newHarness(t, dir, Options{})
	h.store.m[account(t, path)] = []byte("pw")
	h.store.m[account(t, blob)] = []byte("pw")

	h.onlyTerminalColours("the file list")

	h.selectFile("notes.txt")
	h.waitFor("LOCKED")
	h.onlyTerminalColours("a locked file")

	h.selectFile("shared.age")
	h.waitFor("sedit --import")
	h.onlyTerminalColours("an age file")

	h.selectFile("readme.md")
	h.waitFor("Not a sedit file")
	h.onlyTerminalColours("a plain file")

	h.selectFile("projects")
	h.waitFor("items")
	h.onlyTerminalColours("a folder")

	h.press('e') // an error message in the status line
	h.waitFor("select a sedit file first")
	h.onlyTerminalColours("an error message")

	h.selectFile("notes.txt")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	h.onlyTerminalColours("an unlocked file")

	h.press('?')
	h.waitFor("Nothing is decrypted")
	h.onlyTerminalColours("the help box")
	h.key(tcell.KeyEnter)
	h.waitGone("Nothing is decrypted")

	h.press('r')
	h.waitFor("New name or path:")
	h.onlyTerminalColours("an input box")
	h.key(tcell.KeyEscape)
	h.waitGone("New name or path:")

	h.press('d')
	h.waitFor("Delete notes.txt?")
	h.onlyTerminalColours("a confirmation")
	h.key(tcell.KeyEscape)
	h.waitGone("Delete notes.txt?")

	h.selectFile("blob.bin")
	h.key(tcell.KeyEnter)
	h.waitFor("Binary data")
	h.onlyTerminalColours("binary content")
}

func TestPasswordDialogUsesOnlyTheTerminalsOwnColours(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "notes.txt", "hunter2", secretText)
	h := newHarness(t, dir, Options{})
	h.selectFile("notes.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("Password:")
	h.typeText("abc")
	h.waitFor("•••")
	h.onlyTerminalColours("the password dialog")
	h.key(tcell.KeyEnter)
	h.waitFor("Wrong password, try again")
	h.onlyTerminalColours("the password dialog with an error")
}

// Without colour, what stands out must stand out by attribute.
func TestSelectionAndBarsAreInReverseVideo(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "notes.txt", "pw", secretText)
	plainFile(t, dir, "readme.md", "x")
	h := newHarness(t, dir, Options{})
	h.selectFile("notes.txt")
	h.waitFor("LOCKED")
	h.settle()
	g := h.grid()

	// The selected row: its text is reversed; an unselected row's is not.
	reversedAt := func(marker rune) (found, reversed bool) {
		for _, row := range g {
			for x, c := range row {
				if c.r == marker && x < 40 { // in the file list
					return true, c.attr&tcell.AttrReverse != 0
				}
			}
		}
		return false, false
	}
	if found, rev := reversedAt('▪'); !found || !rev {
		t.Errorf("the selected file isn't highlighted (found=%v reversed=%v)", found, rev)
	}
	if found, rev := reversedAt('·'); !found || rev {
		t.Errorf("an unselected file is highlighted (found=%v reversed=%v)", found, rev)
	}

	// The hint bar along the bottom is reversed from edge to edge.
	last := g[len(g)-1]
	for x, c := range last {
		if c.attr&tcell.AttrReverse == 0 {
			t.Fatalf("the status bar isn't reversed at column %d", x)
		}
	}

	// An error is reversed and bold, so it stands out from the plain hints.
	h.selectFile("readme.md")
	h.press('e')
	h.waitFor("select a sedit file first")
	h.settle()
	g = h.grid()
	c := g[len(g)-1][2]
	if c.attr&tcell.AttrReverse == 0 || c.attr&tcell.AttrBold == 0 {
		t.Errorf("an error message should be reversed and bold, got attr=%v", c.attr)
	}
}
