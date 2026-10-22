package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/eddienko/secure-edit/internal/vault"
)

// filterTree makes a small tree of files with and without titles.
func filterTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "work", "deep"), 0o700)
	os.Mkdir(filepath.Join(dir, "personal"), 0o700)
	labelledFile(t, dir, "vpn.txt", "pw", secretText, vault.Meta{Title: "Office VPN login"})
	seditFile(t, dir, "notes.txt", "pw", "x")
	labelledFile(t, filepath.Join(dir, "work"), "bank.txt", "pw", "x", vault.Meta{Title: "Company Bank"})
	seditFile(t, filepath.Join(dir, "work", "deep"), "vpn-backup.txt", "pw", "x")
	seditFile(t, filepath.Join(dir, "personal"), "diary.txt", "pw", "x")
	plainFile(t, dir, "readme.md", "plain")
	return dir
}

func (h *harness) typeFilter(text string) {
	h.t.Helper()
	h.press('/')
	h.typeText(text)
}

func TestFilterByTitleAndNameFindsFilesInClosedFolders(t *testing.T) {
	h := newHarness(t, filterTree(t), Options{})
	h.waitFor("notes.txt")
	h.notOnScreen("vpn-backup.txt") // inside a closed folder

	h.typeFilter("vpn")
	h.waitFor("Office VPN login") // by title
	h.waitFor("vpn-backup.txt")   // by name, two folders down: its folders are opened
	h.waitFor("work/")
	h.waitFor("deep/")
	h.waitGone("notes.txt")
	h.notOnScreen("diary.txt")
	h.notOnScreen("bank.txt")
	h.notOnScreen("readme.md")
	h.notOnScreen("personal/") // no match inside it
	h.waitFor("2 matches")
}

func TestFilterIsCaseInsensitiveAndNeedsEveryWord(t *testing.T) {
	h := newHarness(t, filterTree(t), Options{})
	h.typeFilter("COMPANY bank")
	h.waitFor("Company Bank")
	h.waitFor("1 match")
	h.notOnScreen("Office VPN")

	for i := 0; i < len("COMPANY bank"); i++ {
		h.key(tcell.KeyBackspace2)
	}
	h.typeText("vpn bank") // no single file has both words
	h.waitFor("no match")
	h.waitFor("Nothing matches")
}

func TestFilterWordsMayMixTitleAndName(t *testing.T) {
	h := newHarness(t, filterTree(t), Options{})
	h.typeFilter("office vpn.txt") // one word is in the title, the other in the name
	h.waitFor("Office VPN login")
	h.waitFor("1 match")
}

func TestEnterKeepsTheFilterAndEscapeClearsIt(t *testing.T) {
	h := newHarness(t, filterTree(t), Options{})
	h.typeFilter("diary")
	h.waitFor("diary.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("Esc clear") // the bar tells how to get out
	h.waitFor("/ edit filter")
	h.waitFor("· /diary") // and the list's title says what it is narrowed to
	h.notOnScreen("notes.txt")

	// Typing a letter now is a command again, not more filter.
	h.press('l')
	h.waitFor("locked")
	h.notOnScreen("· /diaryl")

	h.key(tcell.KeyEscape)
	h.waitFor("notes.txt")
	h.waitGone("· /diary")
	h.waitFor("/ filter")
}

func TestEscapeWhileTypingClearsAtOnce(t *testing.T) {
	h := newHarness(t, filterTree(t), Options{})
	h.typeFilter("diary")
	h.waitFor("1 match")
	h.key(tcell.KeyEscape)
	h.waitFor("notes.txt")
	h.waitGone("1 match")
	h.press('l') // back to commands
	h.waitFor("locked")
}

func TestFilterKeysAreNotCommandsWhileTyping(t *testing.T) {
	dir := filterTree(t)
	h := newHarness(t, dir, Options{})
	h.press('/')
	// q quits, n makes a file, d deletes, e edits: none of that while typing.
	h.typeText("qned?")
	h.waitFor("/qned?")
	h.waitFor("no match")
	h.notOnScreen("Keys") // ? did not open the help
	h.notOnScreen("New file")
	select {
	case <-h.done:
		t.Fatal("q quit the program while typing a filter")
	default:
	}
}

func TestBackspaceAndCtrlUEditTheFilter(t *testing.T) {
	h := newHarness(t, filterTree(t), Options{})
	h.typeFilter("diaryx")
	h.waitFor("no match")
	h.key(tcell.KeyBackspace2)
	h.waitFor("diary.txt")
	h.key(tcell.KeyCtrlU)
	h.waitFor("notes.txt") // an empty filter shows everything
	h.waitFor("type to filter")
	h.key(tcell.KeyBackspace2) // nothing to delete: no harm
}

func TestFilterWithUnicodeBackspaceRemovesWholeCharacter(t *testing.T) {
	dir := t.TempDir()
	labelledFile(t, dir, "a.txt", "pw", "x", vault.Meta{Title: "Café ☕"})
	h := newHarness(t, dir, Options{})
	h.typeFilter("café ☕")
	h.waitFor("1 match")
	h.key(tcell.KeyBackspace2)
	h.key(tcell.KeyBackspace2)
	h.waitFor("1 match")
	h.waitFor("/café▏") // both characters went, no broken byte left
}

func TestArrowKeysPickAMatchWhileTyping(t *testing.T) {
	h := newHarness(t, filterTree(t), Options{})
	h.typeFilter("vpn")
	h.waitFor("2 matches")
	cur := func() string {
		var n string
		h.do(func() { e, _ := h.a.current(); n = e.Name })
		return n
	}
	first := cur()
	if first == "" || strings.HasSuffix(first, "/") {
		t.Fatalf("the selection should be on the first match, not %q", first)
	}
	// Folders are listed before files, so the match in work/deep comes first.
	if first != "vpn-backup.txt" {
		t.Fatalf("the first match is %q, want vpn-backup.txt", first)
	}
	h.key(tcell.KeyDown)
	h.settle()
	if got := cur(); got != "vpn.txt" {
		t.Errorf("after Down the selection is %q, want vpn.txt", got)
	}
	h.waitFor("/vpn") // and the filter is still being typed
	h.key(tcell.KeyEnter)
	h.key(tcell.KeyEnter) // unlocks the selected match
	h.waitFor("Password:")
}

func TestTheFirstMatchIsSelectedAndTheOneYouWereOnIsKept(t *testing.T) {
	dir := filterTree(t)
	h := newHarness(t, dir, Options{})
	h.selectFile("vpn.txt")
	h.typeFilter("txt") // matches several; the selection stays on vpn.txt
	h.waitFor("matches")
	var name string
	h.do(func() { e, _ := h.a.current(); name = e.Name })
	if name != "vpn.txt" {
		t.Errorf("the selection moved to %q though it still matches", name)
	}
	h.typeText(" diary") // vpn.txt no longer matches
	h.waitFor("diary.txt")
	h.settle()
	h.do(func() { e, _ := h.a.current(); name = e.Name })
	if name != "diary.txt" {
		t.Errorf("the selection is %q, want the first match", name)
	}
}

func TestMovingToAnotherMatchLocksTheOpenFile(t *testing.T) {
	dir := filterTree(t)
	h := newHarness(t, dir, Options{})
	h.remember(filepath.Join(dir, "vpn.txt"))
	h.selectFile("vpn.txt")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	h.typeFilter("diary") // the open file is filtered away
	h.waitGone(secretText)
	if h.session() != nil {
		t.Error("the open file stayed unlocked after it was filtered out of the list")
	}
}

func TestAMatchCanBeUnlockedAndEdited(t *testing.T) {
	dir := filterTree(t)
	h := newHarness(t, dir, Options{})
	h.remember(filepath.Join(dir, "vpn.txt"))
	useEditor(t, `echo more >> "$1"`+"\n")
	h.typeFilter("office")
	h.key(tcell.KeyEnter)
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	h.press('e')
	h.waitFor("saved vpn.txt")
	h.waitFor("Office VPN login") // still filtered
	if got := decrypt(t, filepath.Join(dir, "vpn.txt"), "pw"); got != secretText+"more\n" {
		t.Errorf("file holds %q", got)
	}
}

func TestANewFileThatDoesNotMatchClearsTheFilter(t *testing.T) {
	dir := filterTree(t)
	h := newHarness(t, dir, Options{})
	useEditor(t, `echo hi >> "$1"`+"\n")
	h.typeFilter("diary")
	h.key(tcell.KeyEnter)
	h.waitFor("diary.txt")
	h.press('n')
	h.waitFor("Name:")
	h.typeText("zzz.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("Choose a password:")
	h.typeText("pw")
	h.key(tcell.KeyEnter)
	h.waitFor("Type it again:")
	h.typeText("pw")
	h.key(tcell.KeyEnter)
	h.waitFor("saved zzz.txt")
	h.waitFor("▫ zzz.txt") // visible: the filter was dropped rather than hide it,
	h.waitFor("personal/") // and the folder it is in is open
	h.waitFor("notes.txt")
	h.waitGone("· /diary")
}

func TestFilterSeesFilesMadeOutsideAfterRefresh(t *testing.T) {
	dir := filterTree(t)
	h := newHarness(t, dir, Options{})
	h.typeFilter("fresh")
	h.waitFor("no match")
	seditFile(t, dir, "fresh.txt", "pw", "x")
	h.key(tcell.KeyEnter)
	h.press('R')
	h.waitFor("fresh.txt")
	h.waitFor("(1 match)") // the kept filter's count is on the bar
}

func TestFilterIsOffered(t *testing.T) {
	dir := filterTree(t)
	h := newHarness(t, dir, Options{})
	h.selectFile("notes.txt")
	h.waitFor("/ filter")
	h.press('?')
	h.waitFor("filter the list by title or name")
}

func TestFilterUsesOnlyTheTerminalsColoursAndHidesPopups(t *testing.T) {
	h := newHarness(t, filterTree(t), Options{})
	h.typeFilter("vpn")
	h.waitFor("2 matches")
	h.onlyTerminalColours("a filtered list")
	h.key(tcell.KeyEnter)
	h.waitFor("Esc clear")
	h.onlyTerminalColours("a kept filter")
}

func TestFilterIgnoresHiddenFilesAndDeepTreesAreBounded(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "vpn.txt.bak", "pw", "old")
	plainFile(t, dir, ".vpn-lock", "")
	seditFile(t, dir, "vpn.txt", "pw", "x")
	got := walk(dir)
	if len(got) != 1 || got[0].Name != "vpn.txt" {
		t.Errorf("walk found %v; backups and dot files are not for the list", got)
	}
	deep := t.TempDir()
	p := deep
	for i := 0; i < maxIndexDeep+5; i++ {
		p = filepath.Join(p, "d")
		os.Mkdir(p, 0o700)
	}
	if n := len(walk(deep)); n > maxIndexDeep+1 {
		t.Errorf("walked %d levels, want at most %d", n, maxIndexDeep+1)
	}
}

func TestMatchesWordsAreAllRequired(t *testing.T) {
	e := Entry{Name: "vpn.txt", Meta: vault.Meta{Title: "Office VPN"}}
	for words, want := range map[string]bool{
		"vpn":        true,
		"office txt": true,
		"bank":       false,
		"vpn bank":   false,
		"":           true,
	} {
		if got := e.matches(strings.Fields(words)); got != want {
			t.Errorf("%q: got %v, want %v", words, got, want)
		}
	}
}
