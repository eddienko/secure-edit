package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/eddienko/secure-edit/internal/vault"
)

// labelledFile makes a sedit file with a title and description.
func labelledFile(t *testing.T, dir, name, password, text string, m vault.Meta) string {
	t.Helper()
	data, err := vault.EncryptMeta([]byte(password), []byte(text), fastKDF, m)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// rowWith returns the cells of the first screen row that contains text.
func (h *harness) rowWith(text string) []cellInfo {
	h.t.Helper()
	for _, row := range h.grid() {
		var b strings.Builder
		for _, c := range row {
			b.WriteRune(c.r)
		}
		if strings.Contains(b.String(), text) {
			return row
		}
	}
	h.t.Fatalf("no row contains %q:\n%s", text, h.screen())
	return nil
}

func TestListShowsTheTitleWithTheFileNameBesideIt(t *testing.T) {
	dir := t.TempDir()
	labelledFile(t, dir, "vpn.txt", "pw", secretText, vault.Meta{Title: "Work VPN login"})
	seditFile(t, dir, "notes.txt", "pw", secretText)
	h := newHarness(t, dir, Options{})
	h.waitFor("▪ Work VPN login")
	h.waitFor("▪ notes.txt") // no title: just the name, as before

	h.settle()
	row := h.rowWith("Work VPN login")
	var line strings.Builder
	for _, c := range row {
		line.WriteRune(c.r)
	}
	if !strings.Contains(line.String(), "Work VPN login  vpn.txt") {
		t.Errorf("the file name should follow the title:\n%s", h.screen())
	}
	// The name is dim and the title is not, so they can be told apart without
	// any colour.
	titleAt := strings.Index(line.String(), "Work")
	nameAt := strings.Index(line.String(), "vpn.txt")
	titleCol, nameCol := len([]rune(line.String()[:titleAt])), len([]rune(line.String()[:nameAt]))
	if row[titleCol].attr&tcell.AttrDim != 0 {
		t.Error("the title is dim")
	}
	if row[nameCol].attr&tcell.AttrDim == 0 {
		t.Error("the file name is not dim")
	}
	h.onlyTerminalColours("a list with titles")
}

func TestTheSelectedTitleStaysReadable(t *testing.T) {
	dir := t.TempDir()
	labelledFile(t, dir, "vpn.txt", "pw", secretText, vault.Meta{Title: "Work VPN login"})
	h := newHarness(t, dir, Options{})
	h.selectFile("vpn.txt")
	h.settle()
	row := h.rowWith("Work VPN login")[:34] // the list; the preview beside it says it again
	for _, c := range row {
		if c.r == 'W' && c.attr&tcell.AttrReverse == 0 {
			t.Fatal("the selected title is not in reverse video")
		}
	}
	// The file name beside it stays in reverse video too, or the highlight
	// would have a hole in it.
	for i, c := range row {
		if c.r == 'v' && i+6 < len(row) && row[i+6].r == 't' && c.attr&tcell.AttrReverse == 0 {
			t.Error("the dimmed file name has lost the selection highlight")
		}
	}
	h.onlyTerminalColours("a selected title")
}

func TestLockedPreviewShowsTheTitleAndDescriptionWithoutAPassword(t *testing.T) {
	dir := t.TempDir()
	labelledFile(t, dir, "vpn.txt", "pw", secretText, vault.Meta{
		Title:       "Work VPN login",
		Description: "Credentials for the office VPN.\nRotate every 90 days.",
	})
	h := newHarness(t, dir, Options{})
	h.selectFile("vpn.txt")
	h.waitFor("Credentials for the office VPN.")
	h.waitFor("Rotate every 90 days.")
	h.waitFor("LOCKED")
	h.waitFor("Press Enter to unlock.")
	if h.session() != nil {
		t.Error("looking at the labels unlocked the file")
	}
	h.notOnScreen(secretText)

	// Unlocking replaces them with the text.
	h.key(tcell.KeyEnter)
	h.waitFor("Password:")
	h.typeText("pw")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	h.waitFor("Rotate every 90 days.") // the description stays, above the text
}

func TestALongDescriptionWraps(t *testing.T) {
	dir := t.TempDir()
	labelledFile(t, dir, "a.txt", "pw", "x", vault.Meta{Description: strings.Repeat("word ", 40) + "THE-END"})
	h := newHarness(t, dir, Options{})
	h.selectFile("a.txt")
	h.waitFor("THE-END")
}

// The labels are readable by anyone, so whatever a file claims is only text:
// no style tags, no control sequences.
func TestHostileLabelsAreShownAsPlainText(t *testing.T) {
	dir := t.TempDir()
	// A title that looks like style tags, which tview would otherwise obey.
	data, err := vault.EncryptMeta([]byte("pw"), []byte("x"), fastKDF, vault.Meta{Title: "[red]Bold[-] [::b]x", Description: "d"})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "tags.txt"), data, 0o600)

	// A header with an escape sequence in the title, patched in because sedit
	// itself refuses to write one.
	good, _ := vault.EncryptMeta([]byte("pw"), []byte("x"), fastKDF, vault.Meta{Title: "AAAA", Description: "BBBB"})
	bad := append([]byte(nil), good...)
	at := strings.Index(string(bad), "AAAA")
	copy(bad[at:], "\x1b]0;")
	os.WriteFile(filepath.Join(dir, "ctl.txt"), bad, 0o600)

	h := newHarness(t, dir, Options{})
	h.waitFor("▪ [red]Bold[-] [::b]x") // shown literally, not interpreted
	h.waitFor("▪ ·]0;")                // the escape is a dot
	h.settle()
	for _, row := range h.grid() {
		for _, c := range row {
			if c.r == 0x1b {
				t.Fatal("an escape character reached the screen")
			}
		}
	}
	h.selectFile("ctl.txt")
	h.waitFor("BBBB")
	h.onlyTerminalColours("hostile labels")
}

func TestNewFileAsksForATitleAndDescription(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, dir, Options{})
	useEditor(t, `echo "brand new text" >> "$1"`+"\n")

	h.press('n')
	h.waitFor("Name:") // all three questions are in one box
	h.waitFor("Title:")
	h.waitFor("Description:")
	h.waitFor("visible while locked")
	h.typeText("vpn.txt")
	h.key(tcell.KeyTab)
	h.typeText("Work VPN")
	h.key(tcell.KeyTab)
	h.typeText("office VPN")
	h.key(tcell.KeyEnter) // a new line in the description, not "create"
	h.typeText("rotate often")
	h.waitFor("rotate often")
	h.notOnScreen("Choose a password:")
	h.key(tcell.KeyCtrlS)
	h.waitFor("Choose a password:")
	h.typeText("s3cret")
	h.key(tcell.KeyEnter)
	h.waitFor("Type it again:")
	h.typeText("s3cret")
	h.key(tcell.KeyEnter)
	h.waitFor("saved vpn.txt")

	path := filepath.Join(dir, "vpn.txt")
	m, err := vault.ReadMeta(readFile(t, path))
	if err != nil || m != (vault.Meta{Title: "Work VPN", Description: "office VPN\nrotate often"}) {
		t.Fatalf("labels in the file: %+v, %v", m, err)
	}
	if got := decrypt(t, path, "s3cret"); got != "brand new text\n" {
		t.Errorf("text %q", got)
	}
	h.waitFor("▪ Work VPN")
	if strings.Contains(string(readFile(t, path)), "brand new text") {
		t.Error("plaintext in the file")
	}
}

func TestNewFileLabelsAreOptional(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, dir, Options{})
	useEditor(t, `echo hi >> "$1"`+"\n")
	h.press('n')
	h.waitFor("Name:")
	h.typeText("plain.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("Choose a password:")
	h.typeText("pw")
	h.key(tcell.KeyEnter)
	h.waitFor("Type it again:")
	h.typeText("pw")
	h.key(tcell.KeyEnter)
	h.waitFor("saved plain.txt")
	data := readFile(t, filepath.Join(dir, "plain.txt"))
	if data[6] != 1 {
		t.Errorf("file version %d: with no labels it must stay version 1", data[6])
	}
	h.waitFor("▪ plain.txt")
}

func TestEscapeInTheNewFileBoxCancelsIt(t *testing.T) {
	for _, field := range []int{0, 1, 2} { // from whichever field it is pressed in
		dir := t.TempDir()
		h := newHarness(t, dir, Options{})
		useEditor(t, `echo hi >> "$1"`+"\n")
		h.press('n')
		h.waitFor("Name:")
		h.typeText("gone.txt")
		for i := 0; i < field; i++ {
			h.key(tcell.KeyTab)
		}
		h.key(tcell.KeyEscape)
		h.waitGone("Name:")
		h.notOnScreen("Choose a password:")
		if _, err := os.Stat(filepath.Join(dir, "gone.txt")); err == nil {
			t.Errorf("Esc in field %d still created the file", field)
		}
	}
}

// The three questions are one box: nothing else pops up between them.
func TestNewFileIsOneBoxNotThree(t *testing.T) {
	h := newHarness(t, t.TempDir(), Options{})
	h.press('n')
	h.waitFor("Name:")
	scr := h.screen()
	for _, want := range []string{"Name:", "Title:", "Description:"} {
		if strings.Count(scr, want) != 1 {
			t.Errorf("%q should be on screen once:\n%s", want, scr)
		}
	}
	if strings.Count(scr, "╔") != 1 {
		t.Errorf("want one box, got %d:\n%s", strings.Count(scr, "╔"), scr)
	}
	h.onlyTerminalColours("the new file box")
	// Enter with a name and nothing else makes the file without labels.
	h.typeText("x.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("Choose a password:")
	h.waitGone("Title:")
}

func TestTheArrowKeysAndTabMoveBetweenTheFields(t *testing.T) {
	h := newHarness(t, t.TempDir(), Options{})
	h.press('n')
	h.waitFor("Name:")
	h.key(tcell.KeyDown) // to Title
	h.typeText("T")
	h.key(tcell.KeyDown) // to Description
	h.typeText("D")
	h.key(tcell.KeyBacktab) // back to Title (Up would move in the description's text)
	h.typeText("2")
	h.key(tcell.KeyUp) // to Name
	h.typeText("N")
	h.key(tcell.KeyTab)
	h.key(tcell.KeyTab)
	h.key(tcell.KeyTab) // wraps round from Description to Name
	h.typeText("M")
	h.waitFor("NM")
	h.waitFor("T2")
	h.waitFor("D")
}

func TestATooLongTitleKeepsTheBoxOpenWithWhatWasTyped(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, dir, Options{})
	h.press('n')
	h.waitFor("Name:")
	h.typeText("a.txt")
	h.key(tcell.KeyTab)
	h.typeText(strings.Repeat("x", vault.MaxTitle+1))
	h.key(tcell.KeyEnter)
	h.waitFor("the title is too long") // said on the bar
	h.waitFor("a.txt")                 // the box is still open with the name in it
	h.waitFor("xxxxxxxx")
	h.notOnScreen("Choose a password:")
	// The focus is on the title: one backspace and Enter is accepted.
	h.key(tcell.KeyBackspace2)
	h.key(tcell.KeyEnter)
	h.waitFor("Choose a password:")
}

func TestABadDescriptionSendsTheFocusToIt(t *testing.T) {
	h := newHarness(t, t.TempDir(), Options{})
	h.press('n')
	h.waitFor("Name:")
	h.typeText("a.txt")
	h.key(tcell.KeyTab)
	h.key(tcell.KeyTab)
	h.typeText(strings.Repeat("d", vault.MaxDescription+1))
	h.key(tcell.KeyTab) // away from it, to Name
	h.key(tcell.KeyCtrlS)
	h.waitFor("the description is too long")
	h.key(tcell.KeyBackspace2) // the focus went back to the description
	h.key(tcell.KeyCtrlS)
	h.waitFor("Choose a password:")
}

func TestInfoKeyChangesTheLabelsAndLeavesTheText(t *testing.T) {
	dir := t.TempDir()
	path := labelledFile(t, dir, "vpn.txt", "pw", secretText, vault.Meta{Title: "Old", Description: "line one\nline two"})
	h := newHarness(t, dir, Options{})
	h.selectFile("vpn.txt")
	h.press('i')
	h.waitFor("Password:") // changing the file needs the password
	h.typeText("pw")
	h.key(tcell.KeyEnter)
	h.waitFor("Title:") // one box with both fields, and no Name
	h.waitFor("Description:")
	h.notOnScreen("Name:")
	h.waitFor("Old")
	h.waitFor("line one") // the description is shown with its own lines
	h.waitFor("line two")
	for range "Old" {
		h.key(tcell.KeyBackspace2)
	}
	h.typeText("New title")
	h.key(tcell.KeyEnter)
	h.waitFor("updated the title and description of vpn.txt")

	m, _ := vault.ReadMeta(readFile(t, path))
	if m != (vault.Meta{Title: "New title", Description: "line one\nline two"}) {
		t.Errorf("labels: %+v", m)
	}
	if got := decrypt(t, path, "pw"); got != secretText {
		t.Errorf("the text changed: %q", got)
	}
	if _, err := os.Stat(vault.BackupPath(path)); err == nil {
		t.Error("changing the labels left a backup")
	}
	h.waitFor("▪ New title")
	h.notOnScreen(secretText) // changing the labels doesn't put the text on screen
}

func TestInfoKeyWithNoChangeWritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := labelledFile(t, dir, "a.txt", "pw", "x", vault.Meta{Title: "Same"})
	before := readFile(t, path)
	h := newHarness(t, dir, Options{})
	h.remember(path)
	h.selectFile("a.txt")
	h.press('i')
	h.waitFor("Title:")
	h.key(tcell.KeyEnter)
	h.waitFor("are unchanged")
	if string(readFile(t, path)) != string(before) {
		t.Error("the file was rewritten")
	}
}

func TestInfoKeyEscapeChangesNothing(t *testing.T) {
	dir := t.TempDir()
	path := labelledFile(t, dir, "a.txt", "pw", "x", vault.Meta{Title: "Same"})
	before := readFile(t, path)
	h := newHarness(t, dir, Options{})
	h.remember(path)
	h.selectFile("a.txt")
	h.press('i')
	h.waitFor("Title:")
	h.typeText("more")
	h.key(tcell.KeyEscape)
	h.waitGone("Title:")
	if string(readFile(t, path)) != string(before) {
		t.Error("the file was rewritten")
	}
}

func TestInfoKeyCanClearTheLabels(t *testing.T) {
	dir := t.TempDir()
	path := labelledFile(t, dir, "a.txt", "pw", "x", vault.Meta{Title: "Gone soon", Description: "d"})
	h := newHarness(t, dir, Options{})
	h.remember(path)
	h.selectFile("a.txt")
	h.press('i')
	h.waitFor("Title:")
	for range "Gone soon" {
		h.key(tcell.KeyBackspace2)
	}
	h.key(tcell.KeyTab)
	h.key(tcell.KeyDelete) // the cursor starts at the beginning of the old text
	h.key(tcell.KeyCtrlS)
	h.waitFor("updated the title and description")
	if data := readFile(t, path); data[6] != 1 {
		t.Errorf("version %d: a file with no labels should be version 1", data[6])
	}
	h.waitFor("▪ a.txt")
}

func TestEditingAndChangingThePasswordKeepTheLabels(t *testing.T) {
	dir := t.TempDir()
	path := labelledFile(t, dir, "a.txt", "pw", "one\n", vault.Meta{Title: "Keep me", Description: "desc"})
	h := newHarness(t, dir, Options{})
	h.remember(path)
	useEditor(t, `echo two >> "$1"`+"\n")
	h.selectFile("a.txt")
	h.press('e')
	h.waitFor("saved a.txt")
	want := vault.Meta{Title: "Keep me", Description: "desc"}
	if m, _ := vault.ReadMeta(readFile(t, path)); m != want {
		t.Errorf("after editing: %+v", m)
	}
	h.press('p')
	h.waitFor("Choose a password:")
	h.typeText("newpw")
	h.key(tcell.KeyEnter)
	h.waitFor("Type it again:")
	h.typeText("newpw")
	h.key(tcell.KeyEnter)
	h.waitFor("password changed")
	if m, _ := vault.ReadMeta(readFile(t, path)); m != want {
		t.Errorf("after changing the password: %+v", m)
	}
	if got := decrypt(t, path, "newpw"); got != "one\ntwo\n" {
		t.Errorf("text %q", got)
	}
}

func TestRenameKeepsTheLabels(t *testing.T) {
	dir := t.TempDir()
	labelledFile(t, dir, "a.txt", "pw", "x", vault.Meta{Title: "Keep me"})
	h := newHarness(t, dir, Options{})
	h.selectFile("a.txt")
	h.press('r')
	h.waitFor("New name or path:")
	for range "a.txt" {
		h.key(tcell.KeyBackspace2)
	}
	h.typeText("b.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("moved to")
	h.waitFor("▪ Keep me")
	h.waitFor("b.txt")
}

func TestInfoIsInTheHintsAndTheHelp(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "notes.txt", "pw", "x")
	h := newHarness(t, dir, Options{})
	h.selectFile("notes.txt")
	h.waitFor("i info")
	h.press('?')
	h.waitFor("change its title and description")
}

func TestSafeText(t *testing.T) {
	if got := safeText("a\x1b[31mb\nc\td", false); got != "a·[31mb c d" {
		t.Errorf("single line: %q", got)
	}
	if got := safeText("a\x07b\nc", true); got != "a·b\nc" {
		t.Errorf("multi line: %q", got)
	}
}

func TestSessionMeta(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	path := labelledFile(t, dir, "a.txt", "pw", "x", vault.Meta{Title: "T"})
	s, err := OpenSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Meta().Title != "T" {
		t.Errorf("Meta() = %+v", s.Meta())
	}
	if err := s.UnlockWith([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta(vault.Meta{Title: "U", Description: "D"}); err != nil {
		t.Fatal(err)
	}
	if s.Meta() != (vault.Meta{Title: "U", Description: "D"}) {
		t.Errorf("after SetMeta: %+v", s.Meta())
	}
	if got := decrypt(t, path, "pw"); got != "x" {
		t.Errorf("text %q", got)
	}
	s.Wipe()
	if err := s.SetMeta(vault.Meta{}); err == nil {
		t.Error("SetMeta worked on a locked session")
	}
}

func TestListReadsTheLabelsFromTheHeaderOnly(t *testing.T) {
	dir := t.TempDir()
	labelledFile(t, dir, "a.txt", "pw", strings.Repeat("big ", 100000), vault.Meta{Title: "T", Description: strings.Repeat("d", vault.MaxDescription)})
	seditFile(t, dir, "b.txt", "pw", "x")
	es, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Entry{}
	for _, e := range es {
		byName[e.Name] = e
	}
	if m := byName["a.txt"].Meta; m.Title != "T" || len(m.Description) != vault.MaxDescription {
		t.Errorf("a.txt: %+v", m)
	}
	if m := byName["b.txt"].Meta; !m.IsZero() {
		t.Errorf("b.txt: %+v", m)
	}
}

// Changing the labels needs the file open in memory, but not on screen: the
// preview must not print the text because someone edited a title.
func TestInfoKeyNeverShowsTheText(t *testing.T) {
	dir := t.TempDir()
	path := labelledFile(t, dir, "vpn.txt", "pw", secretText, vault.Meta{Title: "Old", Description: "about it"})
	h := newHarness(t, dir, Options{})
	h.selectFile("vpn.txt")
	h.press('i')
	h.waitFor("Password:")
	h.typeText("pw")
	h.key(tcell.KeyEnter)
	h.waitFor("Title:")
	h.notOnScreen(secretText) // not behind the box
	h.waitFor("LOCKED")
	h.waitFor("(locked)")
	if s := h.session(); s == nil || s.Locked() {
		t.Fatal("the file should be open in memory while its labels are changed")
	}
	h.typeText("er")
	h.key(tcell.KeyEnter)
	h.waitFor("updated the title and description")
	h.notOnScreen(secretText)
	h.waitFor("LOCKED")
	h.waitFor("Older")
	if s := h.session(); s != nil {
		t.Error("the file stayed open in memory after the change")
	}
	if got := decrypt(t, path, "pw"); got != secretText {
		t.Errorf("the text changed: %q", got)
	}
}

func TestInfoKeyLocksAgainWhenCancelledOrUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := labelledFile(t, dir, "vpn.txt", "pw", secretText, vault.Meta{Title: "Same"})
	h := newHarness(t, dir, Options{})
	h.remember(path)
	h.selectFile("vpn.txt")
	for _, end := range []tcell.Key{tcell.KeyEscape, tcell.KeyEnter} {
		h.press('i')
		h.waitFor("Title:")
		h.notOnScreen(secretText)
		h.key(end)
		h.waitGone("Title:")
		h.notOnScreen(secretText)
		h.waitFor("LOCKED")
		if h.session() != nil {
			t.Errorf("the file stayed open after the box was closed with %v", end)
		}
	}
}

func TestInfoKeyOnAFileAlreadyOpenLeavesItOpen(t *testing.T) {
	dir := t.TempDir()
	path := labelledFile(t, dir, "vpn.txt", "pw", secretText, vault.Meta{Title: "Old"})
	h := newHarness(t, dir, Options{})
	h.remember(path)
	h.selectFile("vpn.txt")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	h.press('i')
	h.waitFor("Title:")
	h.key(tcell.KeyEscape)
	h.waitGone("Title:")
	h.waitFor(secretText)
	if h.session() == nil {
		t.Error("an open file was locked by cancelling the box")
	}
}

// Changing the password needs the file open in memory, not on screen.
func TestChangingThePasswordNeverShowsTheText(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "old", secretText)
	h := newHarness(t, dir, Options{})
	h.selectFile("notes.txt")
	h.press('p')
	h.waitFor("Password:")
	h.typeText("old")
	h.key(tcell.KeyEnter)
	h.waitFor("Choose a password:")
	h.notOnScreen(secretText)
	h.waitFor("LOCKED")
	h.typeText("brandnew")
	h.key(tcell.KeyEnter)
	h.waitFor("Type it again:")
	h.typeText("brandnew")
	h.key(tcell.KeyEnter)
	h.waitFor("password changed for notes.txt")
	h.notOnScreen(secretText)
	h.waitFor("LOCKED")
	if h.session() != nil {
		t.Error("the file stayed open in memory after its password was changed")
	}
	if got := decrypt(t, path, "brandnew"); got != secretText {
		t.Errorf("file holds %q", got)
	}
}

func TestCancellingAPasswordChangeLocksTheFileAgain(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", secretText)
	h := newHarness(t, dir, Options{})
	h.remember(path)
	h.selectFile("notes.txt")
	h.press('p')
	h.waitFor("Choose a password:")
	h.notOnScreen(secretText)
	h.key(tcell.KeyEscape)
	h.waitGone("Choose a password:")
	h.notOnScreen(secretText)
	h.waitFor("LOCKED")
	if h.session() != nil {
		t.Error("the file stayed open after the change was cancelled")
	}
	if got := decrypt(t, path, "pw"); got != secretText {
		t.Errorf("file holds %q", got)
	}
}

func TestChangingThePasswordOfAFileOnScreenLeavesItOnScreen(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", secretText)
	h := newHarness(t, dir, Options{})
	h.remember(path)
	h.selectFile("notes.txt")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	h.press('p')
	h.waitFor("Choose a password:")
	h.key(tcell.KeyEscape)
	h.waitGone("Choose a password:")
	h.waitFor(secretText)
	if h.session() == nil {
		t.Error("cancelling locked a file that was open on screen")
	}
}
