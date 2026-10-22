package tui

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/eddienko/secure-edit/internal/vault"
)

// openBuiltin selects notes.txt (password stored) and opens it in the built-in editor.
func openBuiltin(t *testing.T, opts Options) (*harness, string) {
	t.Helper()
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", "first line")
	h := newHarness(t, dir, opts)
	h.remember(path)
	h.selectFile("notes.txt")
	h.press('E')
	h.waitFor("(editing)")
	h.waitFor("first line")
	return h, path
}

func plainOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := vault.Decrypt([]byte("pw"), data)
	if err != nil {
		t.Fatal(err)
	}
	return string(p)
}

func TestBuiltinEditorEditsAndSaves(t *testing.T) {
	h, path := openBuiltin(t, Options{})
	h.waitFor("^S save")
	h.typeText("NEW ")
	h.waitFor("(editing, not saved)")
	if got := plainOf(t, path); got != "first line" {
		t.Fatalf("nothing is written before Ctrl-S, file holds %q", got)
	}
	h.key(tcell.KeyCtrlS)
	h.waitFor("saved notes.txt (encrypted)")
	h.waitFor("(editing)")
	if got := plainOf(t, path); got != "NEW first line" {
		t.Errorf("saved text = %q", got)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("the previous version should be kept as .bak: %v", err)
	}
	h.key(tcell.KeyEscape) // saved: closes without asking
	h.waitFor("NEW first line")
	h.waitFor("unlocked, locks in")
}

func TestBuiltinEditorAsksBeforeDiscarding(t *testing.T) {
	h, path := openBuiltin(t, Options{})
	h.typeText("X")
	h.key(tcell.KeyEscape)
	h.waitFor("Close without saving?")
	h.key(tcell.KeyEnter) // the default: keep editing
	h.waitFor("(editing, not saved)")

	h.key(tcell.KeyEscape)
	h.waitFor("Close without saving?")
	h.key(tcell.KeyDown)
	h.key(tcell.KeyDown)
	h.key(tcell.KeyEnter) // Discard
	h.waitFor("unlocked, locks in")
	if got := plainOf(t, path); got != "first line" {
		t.Errorf("discarded changes were written: %q", got)
	}
}

func TestBuiltinEditorRefusesWhenTheFileChangedOnDisk(t *testing.T) {
	h, path := openBuiltin(t, Options{})
	other, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Save(path, []byte("pw"), []byte("someone else"), other); err != nil {
		t.Fatal(err)
	}
	h.typeText("X")
	h.key(tcell.KeyCtrlS)
	h.waitFor("was changed on disk")
	if got := plainOf(t, path); got != "someone else" {
		t.Errorf("the other change was overwritten: %q", got)
	}
}

func TestIdleTimerDoesNotLockUnsavedChanges(t *testing.T) {
	h, _ := openBuiltin(t, Options{Idle: 300 * time.Millisecond, TickEvery: 50 * time.Millisecond})
	h.typeText("X")
	time.Sleep(700 * time.Millisecond)
	h.waitFor("(editing, not saved)")
	h.notOnScreen("was locked after")
}

func TestBuiltinEditorLineEdgesIgnoreWrapping(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("word ", 60) // wraps over several rows in the pane
	path := seditFile(t, dir, "notes.txt", "pw", "top\n"+long+"end\nlast")
	h := newHarness(t, dir, Options{})
	h.remember(path)
	h.selectFile("notes.txt")
	h.press('E')
	h.waitFor("(editing)")
	h.key(tcell.KeyDown) // into the long line, on a wrapped row
	h.key(tcell.KeyDown)
	h.key(tcell.KeyCtrlE)
	h.typeText("<E>")
	h.key(tcell.KeyCtrlA)
	h.typeText("<A>")
	h.key(tcell.KeyCtrlS)
	h.waitFor("saved notes.txt")
	want := "top\n<A>" + long + "end<E>\nlast"
	if got := plainOf(t, path); got != want {
		t.Errorf("saved %q, want %q", got, want)
	}
}

func TestBuiltinEditorFindsAndWraps(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", "alpha\nBeta one\ngamma\nbeta two\n")
	h := newHarness(t, dir, Options{})
	h.remember(path)
	h.selectFile("notes.txt")
	h.press('E')
	h.waitFor("(editing)")
	h.waitFor("^F find")

	h.key(tcell.KeyCtrlF)
	h.waitFor("Find")
	h.typeText("beta")
	h.key(tcell.KeyEnter)
	h.typeText("1")    // replaces the selected first match "Beta"
	h.key(tcell.KeyF3) // the second match, the only one left
	h.key(tcell.KeyF3) // nothing after it: round to the start, which is the same one
	h.waitFor("search wrapped round")
	h.typeText("2")
	h.key(tcell.KeyCtrlS)
	h.waitFor("saved notes.txt")
	if got, want := plainOf(t, path), "alpha\n1 one\ngamma\n2 two\n"; got != want {
		t.Errorf("saved %q, want %q", got, want)
	}

	h.key(tcell.KeyCtrlG)
	h.waitFor("Go to line (1-5)")
	h.typeText("3")
	h.key(tcell.KeyEnter)
	h.typeText("#")
	h.key(tcell.KeyCtrlS)
	h.waitFor("saved notes.txt")
	if got, want := plainOf(t, path), "alpha\n1 one\n#gamma\n2 two\n"; got != want {
		t.Errorf("saved %q, want %q", got, want)
	}
}

func TestBuiltinEditorFindMissAndBadLine(t *testing.T) {
	h, _ := openBuiltin(t, Options{})
	h.key(tcell.KeyCtrlF)
	h.typeText("zzz")
	h.key(tcell.KeyEnter)
	h.waitFor("not found: zzz")
	h.waitFor("(editing)") // still editing, with the keys back in the text
	h.key(tcell.KeyCtrlG)
	h.typeText("abc")
	h.key(tcell.KeyEnter)
	h.waitFor("not a line number: abc")
	h.typeText("Q")
	h.waitFor("(editing, not saved)")
}

func TestFindAndLineStart(t *testing.T) {
	if s, e, w := find("Héllo WORLD", "world", 0); s != 7 || e != 12 || w {
		t.Errorf("find = %d %d %v", s, e, w)
	}
	if s, _, w := find("abc abc", "abc", 1); s != 4 || w {
		t.Errorf("next match = %d %v", s, w)
	}
	if s, _, w := find("abc abc", "abc", 5); s != 0 || !w {
		t.Errorf("wrapped match = %d %v", s, w)
	}
	if s, _, _ := find("abc", "x", 0); s != -1 {
		t.Errorf("no match = %d", s)
	}
	if got := lineStart("a\nbb\nccc", 3); got != 5 {
		t.Errorf("lineStart 3 = %d", got)
	}
	if got := lineStart("a\nbb", 9); got != 2 {
		t.Errorf("past the end = %d", got)
	}
}

func TestBuiltinEditorLineNumbers(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("word ", 40)
	path := seditFile(t, dir, "notes.txt", "pw", "alpha\n"+long+"\ngamma")
	h := newHarness(t, dir, Options{})
	h.remember(path)
	h.selectFile("notes.txt")
	h.press('E')
	h.waitFor("(editing)")
	h.notOnScreen("1 alpha") // off to begin with

	h.key(tcell.KeyCtrlT)
	h.waitFor("line numbers on")
	h.waitFor("1 alpha")
	h.waitFor("3 gamma") // the long line is one row, so the third line is the third row
	h.onlyTerminalColours("the built-in editor with line numbers")
	h.key(tcell.KeyCtrlT)
	h.waitFor("line numbers off")
	h.notOnScreen("1 alpha")
	h.waitFor("alpha")
}

// brokenClip is a clipboard that can't be used, as on a machine with no tool.
type brokenClip struct{}

func (brokenClip) Set([]byte) error     { return errors.New("no clipboard tool") }
func (brokenClip) Get() ([]byte, error) { return nil, errors.New("no clipboard tool") }
func (brokenClip) Clear() error         { return nil }

func openForClipboard(t *testing.T, opts Options, text string) (*harness, string) {
	t.Helper()
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", text)
	h := newHarness(t, dir, opts)
	h.remember(path)
	h.selectFile("notes.txt")
	h.press('E')
	h.waitFor("(editing)")
	h.key(tcell.KeyHome)
	return h, path
}

func (h *harness) shiftRight(n int) {
	for i := 0; i < n; i++ {
		h.sim.InjectKey(tcell.KeyRight, 0, tcell.ModShift)
	}
}

func TestBuiltinEditorCopiesAndPastesThroughTheSystemClipboard(t *testing.T) {
	clipb := &fakeClip{}
	h, path := openForClipboard(t, Options{Clipboard: clipb, ClipboardTTL: time.Hour}, "hello world")
	h.shiftRight(5) // select "hello"
	h.key(tcell.KeyCtrlQ)
	h.waitFor("copied to the clipboard")
	if got := clipb.text(); got != "hello" {
		t.Fatalf("clipboard = %q", got)
	}
	h.key(tcell.KeyEnd)
	h.key(tcell.KeyCtrlV)
	h.key(tcell.KeyCtrlS)
	h.waitFor("saved notes.txt")
	if got := plainOf(t, path); got != "hello worldhello" {
		t.Errorf("saved %q", got)
	}

	// Text from outside is pasted too, and a cut leaves the clipboard holding it.
	clipb.Set([]byte("OUT"))
	h.key(tcell.KeyHome)
	h.key(tcell.KeyCtrlV)
	h.shiftRight(3)
	h.key(tcell.KeyCtrlX)
	h.waitFor("copied to the clipboard")
	if got := clipb.text(); got != "hel" {
		t.Errorf("after the cut the clipboard = %q", got)
	}
	h.key(tcell.KeyCtrlS)
	h.waitFor("saved notes.txt")
	if got := plainOf(t, path); got != "OUTlo worldhello" {
		t.Errorf("saved %q", got)
	}
}

func TestBuiltinEditorCopyWithNothingSelectedDoesNothing(t *testing.T) {
	clipb := &fakeClip{}
	clipb.Set([]byte("keep me"))
	h, _ := openForClipboard(t, Options{Clipboard: clipb, ClipboardTTL: time.Hour}, "hello")
	h.key(tcell.KeyCtrlQ)
	h.typeText("x")
	h.waitFor("(editing, not saved)")
	if got := clipb.text(); got != "keep me" {
		t.Errorf("clipboard = %q", got)
	}
}

func TestBuiltinEditorFallsBackToItsOwnClipboard(t *testing.T) {
	h, path := openForClipboard(t, Options{Clipboard: brokenClip{}}, "abc")
	h.shiftRight(2)
	h.key(tcell.KeyCtrlQ)
	h.waitFor("copied inside the editor only")
	h.key(tcell.KeyEnd)
	h.key(tcell.KeyCtrlV)
	h.key(tcell.KeyCtrlS)
	h.waitFor("saved notes.txt")
	if got := plainOf(t, path); got != "abcab" {
		t.Errorf("saved %q", got)
	}
}

func TestShiftHomeAndEndExtendTheSelection(t *testing.T) {
	clipb := &fakeClip{}
	h, _ := openForClipboard(t, Options{Clipboard: clipb, ClipboardTTL: time.Hour}, "hello world")
	h.sim.InjectKey(tcell.KeyEnd, 0, tcell.ModShift)
	h.key(tcell.KeyCtrlQ)
	h.waitFor("copied to the clipboard")
	if got := clipb.text(); got != "hello world" {
		t.Errorf("clipboard = %q", got)
	}
}

// reversedText is the text on the screen that is drawn in reverse video, row by row.
func (h *harness) reversedText() string {
	var out strings.Builder
	for _, row := range h.grid() {
		for _, c := range row {
			if c.attr&tcell.AttrReverse != 0 {
				out.WriteRune(c.r)
			}
		}
	}
	return out.String()
}

func TestSelectedTextShowsInReverse(t *testing.T) {
	h, _ := openForClipboard(t, Options{Clipboard: &fakeClip{}, ClipboardTTL: time.Hour}, "hello world")
	h.shiftRight(5)
	h.settle()
	// The bar at the bottom is reversed too, so look for the word among it.
	if got := h.reversedText(); !strings.Contains(got, "hello") || strings.Contains(got, "world") {
		t.Errorf("the selection should show in reverse: %q", got)
	}
	h.key(tcell.KeyRight) // the selection ends
	h.settle()
	if got := h.reversedText(); strings.Contains(got, "hello") {
		t.Errorf("no selection, but %q is still in reverse", got)
	}
}

func TestSelectionAcrossLinesCopiesAll(t *testing.T) {
	clipb := &fakeClip{}
	h, _ := openForClipboard(t, Options{Clipboard: clipb, ClipboardTTL: time.Hour}, "one\ntwo\nthree\nfour")
	h.sim.InjectKey(tcell.KeyDown, 0, tcell.ModShift)
	h.sim.InjectKey(tcell.KeyDown, 0, tcell.ModShift)
	h.sim.InjectKey(tcell.KeyRight, 0, tcell.ModShift)
	h.settle()
	t.Logf("reversed: %q", h.reversedText())
	h.key(tcell.KeyCtrlQ)
	h.waitFor("copied to the clipboard")
	if got := clipb.text(); got != "one\ntwo\nt" {
		t.Errorf("clipboard = %q", got)
	}
}

func TestMarkingSelectsWithThePlainArrows(t *testing.T) {
	clipb := &fakeClip{}
	h, _ := openForClipboard(t, Options{Clipboard: clipb, ClipboardTTL: time.Hour}, "one\ntwo\nthree\nfour")
	h.key(tcell.KeyCtrlB)
	h.waitFor("(selecting)")
	h.waitFor("the arrow keys select now")
	h.key(tcell.KeyDown)
	h.key(tcell.KeyDown)
	h.key(tcell.KeyRight)
	h.settle()
	if got := h.reversedText(); !strings.Contains(got, "one") || !strings.Contains(got, "two") {
		t.Errorf("the lines should show selected: %q", got)
	}
	h.key(tcell.KeyCtrlQ)
	h.waitFor("copied to the clipboard")
	h.waitFor("(editing)") // copying ends the selecting
	if got := clipb.text(); got != "one\ntwo\nt" {
		t.Errorf("clipboard = %q", got)
	}
}

func TestMarkingEscapeCancelsAndCutDeletes(t *testing.T) {
	clipb := &fakeClip{}
	h, path := openForClipboard(t, Options{Clipboard: clipb, ClipboardTTL: time.Hour}, "one\ntwo\nthree")
	h.key(tcell.KeyCtrlB)
	h.key(tcell.KeyDown)
	h.key(tcell.KeyEscape) // cancels, and the editor stays open
	h.waitFor("(editing)")
	h.settle()
	if got := h.reversedText(); strings.Contains(got, "one") {
		t.Errorf("the selection should be gone: %q", got)
	}
	h.key(tcell.KeyUp) // the cursor was left where the selection ended
	h.key(tcell.KeyHome)
	h.key(tcell.KeyCtrlB)
	h.key(tcell.KeyDown)
	h.key(tcell.KeyCtrlX)
	h.waitFor("(editing, not saved)")
	h.key(tcell.KeyCtrlS)
	h.waitFor("saved notes.txt")
	if got := plainOf(t, path); got != "two\nthree" {
		t.Errorf("saved %q", got)
	}
	if got := clipb.text(); got != "one\n" {
		t.Errorf("clipboard = %q", got)
	}
}

func TestEditorBarDoesNotOfferHelpOrQuit(t *testing.T) {
	h, _ := openBuiltin(t, Options{})
	h.waitFor("^S save")
	h.notOnScreen("? help")
	h.notOnScreen("q quit")
	h.typeText("q?")
	h.waitFor("(editing, not saved)") // they were typed into the text
	h.key(tcell.KeyCtrlB)
	h.waitFor("the arrow keys select now")
	h.key(tcell.KeyEscape)
	h.notOnScreen("q quit")
	h.key(tcell.KeyCtrlS)
	h.waitFor("saved notes.txt")
	h.key(tcell.KeyEscape)
	h.waitFor("? help  q quit") // back in the list
}

func TestF1ListsTheEditorKeysAndReturnsToTheText(t *testing.T) {
	h, _ := openBuiltin(t, Options{})
	h.waitFor("F1 keys")
	h.key(tcell.KeyF1)
	h.waitFor("Editor keys")
	h.waitFor("Ctrl-B    select with the arrow keys")
	h.key(tcell.KeyEscape)
	h.waitGone("Editor keys")
	h.typeText("Z") // the keys go to the text again
	h.waitFor("(editing, not saved)")
	h.waitFor("Zfirst line")
}
