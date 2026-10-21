package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/eddienko/secure-edit/internal/keystore"
	"github.com/eddienko/secure-edit/internal/vault"
)

// folderLabelled makes a folder with a title and description.
func folderLabelled(t *testing.T, parent, name string, m vault.Meta) string {
	t.Helper()
	p := filepath.Join(parent, name)
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := vault.WriteFolderMeta(p, m); err != nil {
		t.Fatal(err)
	}
	return p
}

func (h *harness) selected() (name string) {
	h.settle()
	h.do(func() { e, _ := h.a.current(); name = e.Name })
	return name
}

// boxLines are the rows of the dialog that has the focus (drawn with double
// lines), from its left border to its right.
func (h *harness) boxLines() []string {
	var out []string
	for _, row := range strings.Split(h.screen(), "\n") {
		if i, j := strings.Index(row, "║"), strings.LastIndex(row, "║"); i >= 0 && j > i {
			out = append(out, row[i:j])
		}
	}
	return out
}

func (h *harness) storedFor(path string) string {
	a, err := keystore.Account(path)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(h.store.m[a])
}

// ---- how folders look -----------------------------------------------------

func TestAFolderWithATitleIsListedByIt(t *testing.T) {
	dir := t.TempDir()
	folderLabelled(t, dir, "banking", vault.Meta{Title: "Banking and cards", Description: "All the money things.\nSecond line."})
	os.Mkdir(filepath.Join(dir, "plain"), 0o700)
	h := newHarness(t, dir, Options{})
	h.waitFor("Banking and cards  banking/")
	h.waitFor("plain/") // no title: just the name, as before
	h.selectFile("banking/")
	h.waitFor("Banking and cards · banking/") // the pane's title
	h.waitFor("All the money things.")
	h.waitFor("Second line.")
	h.waitFor("Empty folder.")

	// The name after the title is dim, the title is not.
	h.settle()
	row := h.rowWith("Banking and cards  banking/")[:34]
	var line strings.Builder
	for _, c := range row {
		line.WriteRune(c.r)
	}
	nameAt := len([]rune(line.String()[:strings.Index(line.String(), "banking/")]))
	if row[nameAt].attr&tcell.AttrDim == 0 {
		t.Error("the folder name after the title is not dim")
	}
	h.onlyTerminalColours("a folder with a title")
}

func TestFoldersAreSortedByTheTitleShown(t *testing.T) {
	dir := t.TempDir()
	folderLabelled(t, dir, "aaa", vault.Meta{Title: "Zulu"})
	folderLabelled(t, dir, "zzz", vault.Meta{Title: "Alpha"})
	os.Mkdir(filepath.Join(dir, "mmm"), 0o700)
	es, _ := List(dir)
	var got []string
	for _, e := range es {
		got = append(got, e.Name)
	}
	if strings.Join(got, " ") != "zzz mmm aaa" {
		t.Errorf("order %v, want zzz mmm aaa (Alpha, mmm, Zulu)", got)
	}
}

func TestTheFolderPreviewSaysWhatIsInIt(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "mix")
	os.MkdirAll(filepath.Join(sub, "inner"), 0o700)
	seditFile(t, sub, "a.txt", "pw", "x")
	seditFile(t, sub, "b.txt", "pw", "x")
	plainFile(t, sub, "readme.md", "plain")
	plainFile(t, sub, ".DS_Store", "finder") // hidden: not counted
	h := newHarness(t, dir, Options{})
	h.selectFile("mix/")
	h.waitFor("2 sedit files, 1 other file, 1 folder")
	h.waitFor("N new folder")
	h.waitFor("r rename")
	h.waitFor("d delete (when empty)")
}

func TestTheTopFolderOffersNoRenameMoveOrDelete(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "sub"), 0o700)
	h := newHarness(t, dir, Options{})
	h.key(tcell.KeyHome) // the top folder
	h.waitFor("Enter opens or closes it")
	h.notOnScreen("r rename")
	for _, key := range []rune{'r', 'm', 'd'} {
		h.press(key)
		h.waitFor("the folder you opened can't be")
		h.key(tcell.KeyEscape)
	}
}

// ---- making folders -------------------------------------------------------

func TestNewFolderIsOneBoxAndMakesAPrivateFolder(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, dir, Options{})
	h.press('N')
	h.waitFor("Name:")
	h.waitFor("Title:")
	h.waitFor("Description:")
	h.waitFor("stored in the clear")
	h.onlyTerminalColours("the new folder box")
	h.typeText("bank")
	h.key(tcell.KeyTab)
	h.typeText("Banking")
	h.key(tcell.KeyTab)
	h.typeText("accounts")
	h.key(tcell.KeyEnter)
	h.typeText("and cards")
	h.key(tcell.KeyCtrlS)
	h.waitFor("made folder bank/")
	path := filepath.Join(dir, "bank")
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("folder: %v %v", fi, err)
	}
	if m := vault.ReadFolderMeta(path); m != (vault.Meta{Title: "Banking", Description: "accounts\nand cards"}) {
		t.Errorf("labels %+v", m)
	}
	h.waitFor("Banking  bank/")
	if got := h.selected(); got != "bank" {
		t.Errorf("selected %q, want the new folder", got)
	}
}

func TestNewFolderWithOnlyANameAndInsideTheSelectedFolder(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "work"), 0o700)
	h := newHarness(t, dir, Options{})
	h.selectFile("work/")
	h.press('N')
	h.waitFor("Name:")
	h.typeText("deep")
	h.key(tcell.KeyEnter)
	h.waitFor("made folder deep/")
	if _, err := os.Stat(filepath.Join(dir, "work", "deep")); err != nil {
		t.Error("not made inside the selected folder")
	}
	if _, err := os.Stat(filepath.Join(dir, "work", "deep", vault.FolderMetaName)); err == nil {
		t.Error("a label file for no labels")
	}
	h.waitFor("deep/") // and the parent is open so it shows
}

func TestNewFolderRefusalsKeepTheBoxOpen(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "taken"), 0o700)
	h := newHarness(t, dir, Options{})
	for name, want := range map[string]string{
		"a/b":     "not a path",
		".hidden": "hidden from the list",
		"taken":   "already exists",
	} {
		h.press('N')
		h.waitFor("Name:")
		h.typeText(name)
		h.key(tcell.KeyEnter)
		h.waitFor(want)
		h.waitFor("Name:") // still open
		h.key(tcell.KeyEscape)
		h.waitGone("Name:")
	}
	h.press('N')
	h.waitFor("Name:")
	h.key(tcell.KeyEnter) // no name at all
	h.waitFor("give it a name")
	h.key(tcell.KeyEscape)
}

func TestEscapeInTheNewFolderBoxMakesNothing(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, dir, Options{})
	h.press('N')
	h.waitFor("Name:")
	h.typeText("nope")
	h.key(tcell.KeyEscape)
	h.waitGone("Name:")
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Errorf("made %v", es)
	}
}

// ---- changing a folder's labels ------------------------------------------

func TestInfoOnAFolderNeedsNoPassword(t *testing.T) {
	dir := t.TempDir()
	path := folderLabelled(t, dir, "bank", vault.Meta{Title: "Old", Description: "line one"})
	h := newHarness(t, dir, Options{})
	h.selectFile("bank/")
	h.press('i')
	h.waitFor("Title:") // straight to the box
	h.notOnScreen("Password")
	h.waitFor("Old")
	for range "Old" {
		h.key(tcell.KeyBackspace2)
	}
	h.typeText("New")
	h.key(tcell.KeyEnter)
	h.waitFor("updated the title and description of bank/")
	if m := vault.ReadFolderMeta(path); m != (vault.Meta{Title: "New", Description: "line one"}) {
		t.Errorf("labels %+v", m)
	}
	h.waitFor("New  bank/")
}

func TestInfoOnAFolderCanClearAndKeepsOnEscape(t *testing.T) {
	dir := t.TempDir()
	path := folderLabelled(t, dir, "bank", vault.Meta{Title: "Gone"})
	h := newHarness(t, dir, Options{})
	h.selectFile("bank/")
	h.press('i')
	h.waitFor("Title:")
	h.typeText("more")
	h.key(tcell.KeyEscape)
	h.waitGone("Title:")
	if m := vault.ReadFolderMeta(path); m.Title != "Gone" {
		t.Errorf("Esc changed the title to %q", m.Title)
	}
	h.press('i')
	h.waitFor("Title:")
	h.key(tcell.KeyEnter)
	h.waitFor("are unchanged")
	h.press('i')
	h.waitFor("Title:")
	for range "Gone" {
		h.key(tcell.KeyBackspace2)
	}
	h.key(tcell.KeyEnter)
	h.waitFor("updated the title")
	if _, err := os.Stat(filepath.Join(path, vault.FolderMetaName)); err == nil {
		t.Error("clearing the labels left the label file")
	}
	h.waitFor("bank/")
}

// ---- renaming a folder ----------------------------------------------------

func TestRenameAFolderKeepsEverythingInItAndItsOpenState(t *testing.T) {
	dir := t.TempDir()
	work := folderLabelled(t, dir, "work", vault.Meta{Title: "Work"})
	os.Mkdir(filepath.Join(work, "deep"), 0o700)
	a := seditFile(t, work, "a.txt", "pw", "x")
	b := seditFile(t, filepath.Join(work, "deep"), "b.txt", "pw", "y")
	seditFile(t, work, "a.txt.bak", "pw", "old")
	h := newHarness(t, dir, Options{})
	h.remember(a)
	h.remember(b)
	h.selectFile("work/")
	h.key(tcell.KeyRight) // open it, and its subfolder
	h.waitFor("deep/")
	h.selectFile("deep/")
	h.key(tcell.KeyRight)
	h.waitFor("b.txt")
	h.selectFile("work/")

	h.press('r')
	h.waitFor("New name:")
	for range "work" {
		h.key(tcell.KeyBackspace2)
	}
	h.typeText("job")
	h.key(tcell.KeyEnter)
	h.waitFor("renamed to job/ (with 2 sedit files and 2 stored passwords)")

	job := filepath.Join(dir, "job")
	for _, f := range []string{"a.txt", "a.txt.bak", "deep/b.txt"} {
		if _, err := os.Stat(filepath.Join(job, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if _, err := os.Stat(work); err == nil {
		t.Error("the old folder is still there")
	}
	if got := h.storedFor(filepath.Join(job, "a.txt")); got != "pw" {
		t.Errorf("stored password of a.txt: %q", got)
	}
	if got := h.storedFor(filepath.Join(job, "deep", "b.txt")); got != "pw" {
		t.Errorf("stored password of deep/b.txt: %q", got)
	}
	if got := h.storedFor(a); got != "" {
		t.Errorf("the old entry is still stored: %q", got)
	}
	h.waitFor("▪ a.txt") // still open, with its subfolder
	h.waitFor("▪ b.txt")
	h.waitFor("Work  job/") // the title came along
	if got := h.selected(); got != "job" {
		t.Errorf("selected %q", got)
	}
}

func TestRenameAFolderLocksTheOpenFileInsideIt(t *testing.T) {
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	os.Mkdir(work, 0o700)
	a := seditFile(t, work, "a.txt", "pw", secretText)
	h := newHarness(t, dir, Options{})
	h.remember(a)
	h.selectFile("work/")
	h.key(tcell.KeyRight)
	h.selectFile("a.txt")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	h.selectFile("work/")
	h.press('r')
	h.waitFor("New name:")
	h.typeText("2")
	h.key(tcell.KeyEnter)
	h.waitFor("renamed to work2/")
	h.notOnScreen(secretText)
	if h.session() != nil {
		t.Error("a file inside the folder stayed open")
	}
}

func TestRenameAFolderRefusals(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "a"), 0o700)
	os.Mkdir(filepath.Join(dir, "b"), 0o700)
	h := newHarness(t, dir, Options{})
	h.selectFile("a/")
	for name, want := range map[string]string{
		"b":    "refusing to overwrite",
		"x/y":  "not a path",
		".dot": "hidden from the list",
	} {
		h.press('r')
		h.waitFor("New name:")
		h.key(tcell.KeyCtrlU)
		for range "a" {
			h.key(tcell.KeyBackspace2)
		}
		h.typeText(name)
		h.key(tcell.KeyEnter)
		h.waitFor(want)
	}
	if _, err := os.Stat(filepath.Join(dir, "a")); err != nil {
		t.Error("a refusal changed the folder")
	}
}

func TestKeysAreIgnoredWhileAFolderMoves(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "a"), 0o700)
	h := newHarness(t, dir, Options{})
	h.do(func() { h.a.busy = true })
	h.press('q')
	h.press('N')
	h.settle()
	select {
	case <-h.done:
		t.Fatal("q quit while a folder was moving")
	default:
	}
	h.notOnScreen("Name:")
	h.do(func() { h.a.busy = false })
	h.press('N')
	h.waitFor("Name:")
}

// ---- moving ---------------------------------------------------------------

func TestMoveAFileToAFolderWithThePicker(t *testing.T) {
	dir := t.TempDir()
	folderLabelled(t, dir, "work", vault.Meta{Title: "Work stuff"})
	os.MkdirAll(filepath.Join(dir, "work", "deep"), 0o700)
	os.Mkdir(filepath.Join(dir, "personal"), 0o700)
	f := seditFile(t, dir, "vpn.txt", "pw", "x")
	seditFile(t, dir, "vpn.txt.bak", "pw", "old")
	h := newHarness(t, dir, Options{})
	h.remember(f)
	h.selectFile("vpn.txt")
	h.press('m')
	h.waitFor("Move vpn.txt to")
	h.waitFor("work/")
	h.waitFor("Work stuff") // titles are shown beside the path
	h.waitFor("work/deep/")
	h.waitFor("personal/")
	h.onlyTerminalColours("the move picker")
	h.typeText("deep")
	h.settle()
	if box := strings.Join(h.boxLines(), "\n"); strings.Contains(box, "personal/") || !strings.Contains(box, "work/deep/") {
		t.Errorf("typing should narrow the choices to work/deep/:\n%s", box)
	}
	h.key(tcell.KeyEnter)
	h.waitFor("moved vpn.txt to work/deep/")

	dest := filepath.Join(dir, "work", "deep", "vpn.txt")
	if _, err := os.Stat(dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest + ".bak"); err != nil {
		t.Error("the backup did not come")
	}
	if got := h.storedFor(dest); got != "pw" {
		t.Errorf("stored password at the new path: %q", got)
	}
	if got := h.storedFor(f); got != "" {
		t.Errorf("stored password at the old path: %q", got)
	}
	h.waitFor("▪ vpn.txt") // visible: its folders were opened
	if got := h.selected(); got != "vpn.txt" {
		t.Errorf("selected %q", got)
	}
}

func TestThePickerLeavesOutWhereTheFileAlreadyIs(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "work"), 0o700)
	seditFile(t, filepath.Join(dir, "work"), "a.txt", "pw", "x")
	h := newHarness(t, dir, Options{})
	h.selectFile("work/")
	h.key(tcell.KeyRight)
	h.selectFile("a.txt")
	h.press('m')
	h.waitFor("Move a.txt to")
	h.waitFor("/  (this whole folder)") // the top folder is offered
	if box := strings.Join(h.boxLines(), "\n"); strings.Contains(box, "work/") {
		t.Errorf("the folder the file is already in is offered:\n%s", box)
	}
	h.key(tcell.KeyDown) // there is only one choice: down stays
	h.key(tcell.KeyEnter)
	h.waitFor("moved a.txt to /")
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Error("not moved to the top folder")
	}
}

func TestMoveWithNoOtherFolder(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "a.txt", "pw", "x")
	h := newHarness(t, dir, Options{})
	h.selectFile("a.txt")
	h.press('m')
	h.waitFor("no other folder to move it to; make one with N")
}

func TestEscapeAndNoMatchInThePicker(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "work"), 0o700)
	seditFile(t, dir, "a.txt", "pw", "x")
	h := newHarness(t, dir, Options{})
	h.selectFile("a.txt")
	h.press('m')
	h.waitFor("Move a.txt to")
	h.typeText("zzz") // nothing matches: Enter does nothing
	h.key(tcell.KeyEnter)
	h.settle()
	h.waitFor("Move a.txt to")
	h.key(tcell.KeyCtrlU)
	h.waitFor("work/")
	h.key(tcell.KeyEscape)
	h.waitGone("Move a.txt to")
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Error("Esc moved the file")
	}
}

func TestMoveAFileWontOverwrite(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "work"), 0o700)
	seditFile(t, dir, "a.txt", "pw", "mine")
	seditFile(t, filepath.Join(dir, "work"), "a.txt", "pw", "theirs")
	h := newHarness(t, dir, Options{})
	h.selectFile("a.txt")
	h.press('m')
	h.waitFor("Move a.txt to")
	h.key(tcell.KeyEnter) // the first choice is work/ (the top is where it is)
	h.waitFor("refusing to overwrite")
	if got := decrypt(t, filepath.Join(dir, "work", "a.txt"), "pw"); got != "theirs" {
		t.Errorf("overwritten: %q", got)
	}
}

func TestMoveAFolderWithItsFilesAndPasswords(t *testing.T) {
	dir := t.TempDir()
	work := folderLabelled(t, dir, "work", vault.Meta{Title: "Work"})
	os.Mkdir(filepath.Join(work, "deep"), 0o700)
	os.Mkdir(filepath.Join(dir, "archive"), 0o700)
	a := seditFile(t, work, "a.txt", "pw", "x")
	b := seditFile(t, filepath.Join(work, "deep"), "b.txt", "pw", "y")
	h := newHarness(t, dir, Options{})
	h.remember(a)
	h.remember(b)
	h.selectFile("work/")
	h.press('m')
	h.waitFor("Move work/ to")
	h.waitFor("archive/")
	// Not into itself or what is inside it, and not where it already is.
	if box := strings.Join(h.boxLines(), "\n"); strings.Contains(box, "work/") || strings.Contains(box, "this whole folder") {
		t.Errorf("a folder is offered somewhere it can't or needn't go:\n%s", box)
	}
	h.key(tcell.KeyEnter)
	h.waitFor("moved work/ (with 2 sedit files and 2 stored passwords)")

	moved := filepath.Join(dir, "archive", "work")
	if got := h.storedFor(filepath.Join(moved, "deep", "b.txt")); got != "pw" {
		t.Errorf("stored password of b.txt: %q", got)
	}
	if m := vault.ReadFolderMeta(moved); m.Title != "Work" {
		t.Errorf("labels did not come: %+v", m)
	}
	h.waitFor("Work  work/")
	if got := h.selected(); got != "work" {
		t.Errorf("selected %q", got)
	}
}

// ---- deleting -------------------------------------------------------------

func TestDeleteAnEmptyFolderAsksFirstAndCancelIsTheDefault(t *testing.T) {
	dir := t.TempDir()
	empty := folderLabelled(t, dir, "empty", vault.Meta{Title: "Nothing here"})
	os.WriteFile(filepath.Join(empty, ".DS_Store"), []byte("finder"), 0o600)
	h := newHarness(t, dir, Options{})
	h.selectFile("empty/")
	h.press('d')
	h.waitFor("Delete the empty folder empty/?")
	h.key(tcell.KeyEnter) // the focused button is Cancel
	h.waitGone("Delete the empty folder")
	if _, err := os.Stat(empty); err != nil {
		t.Fatal("Enter deleted the folder: Cancel must be the default")
	}
	h.press('d')
	h.waitFor("Delete the empty folder")
	h.key(tcell.KeyRight)
	h.key(tcell.KeyEnter)
	h.waitFor("deleted folder empty/")
	if _, err := os.Stat(empty); err == nil {
		t.Error("the folder is still there")
	}
	h.waitGone("Nothing here")
}

func TestDeleteAFolderThatIsNotEmptyIsRefusedWithWhatIsInIt(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "full")
	os.Mkdir(full, 0o700)
	seditFile(t, full, "a.txt", "pw", "x")
	plainFile(t, full, "readme.md", "plain")
	h := newHarness(t, dir, Options{})
	h.selectFile("full/")
	h.press('d')
	h.waitFor("full is not empty: it holds a.txt, readme.md; delete or move it first")
	h.notOnScreen("Delete the empty folder")
	if _, err := os.Stat(filepath.Join(full, "a.txt")); err != nil {
		t.Error("something was deleted")
	}
}

func TestDeleteAFolderThatHoldsOnlyAHiddenFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	d := filepath.Join(dir, "d")
	os.Mkdir(d, 0o700)
	plainFile(t, d, ".git", "x")
	h := newHarness(t, dir, Options{})
	h.selectFile("d/")
	h.press('d')
	h.waitFor("d is not empty: it holds .git")
}

func TestDeletingTheLastFileThenItsFolder(t *testing.T) {
	dir := t.TempDir()
	d := filepath.Join(dir, "d")
	os.Mkdir(d, 0o700)
	f := seditFile(t, d, "a.txt", "pw", "x")
	h := newHarness(t, dir, Options{})
	h.remember(f)
	h.selectFile("d/")
	h.key(tcell.KeyRight)
	h.selectFile("a.txt")
	h.press('d')
	h.waitFor("Delete a.txt?")
	h.key(tcell.KeyRight)
	h.key(tcell.KeyEnter)
	h.waitFor("deleted a.txt")
	h.selectFile("d/")
	h.press('d')
	h.waitFor("Delete the empty folder d/?")
	h.key(tcell.KeyRight)
	h.key(tcell.KeyEnter)
	h.waitFor("deleted folder d/")
	if _, err := os.Stat(d); err == nil {
		t.Error("the folder is still there")
	}
}

// ---- the filter -----------------------------------------------------------

func TestFilterShowsEverythingInAFolderThatMatches(t *testing.T) {
	dir := t.TempDir()
	bank := folderLabelled(t, dir, "x1", vault.Meta{Title: "Banking"})
	os.Mkdir(filepath.Join(bank, "cards"), 0o700)
	seditFile(t, bank, "visa.txt", "pw", "x")
	seditFile(t, filepath.Join(bank, "cards"), "amex.txt", "pw", "x")
	seditFile(t, dir, "notes.txt", "pw", "x")
	seditFile(t, dir, "bank-note.txt", "pw", "x") // matches by name, in the top folder
	h := newHarness(t, dir, Options{})
	h.typeFilter("bank")
	h.waitFor("Banking  x1/") // matched by title
	h.waitFor("visa.txt")     // does not match "bank", but is in a folder that does
	h.waitFor("cards/")
	h.waitFor("amex.txt")
	h.waitFor("bank-note.txt")
	h.waitGone("notes.txt")
	h.waitFor("2 matches") // the folder and the file; what is merely inside is not counted
}

func TestFilterByAFolderNameToo(t *testing.T) {
	dir := t.TempDir()
	d := filepath.Join(dir, "invoices")
	os.Mkdir(d, 0o700)
	seditFile(t, d, "march.txt", "pw", "x")
	seditFile(t, dir, "other.txt", "pw", "x")
	h := newHarness(t, dir, Options{})
	h.typeFilter("invoice")
	h.waitFor("invoices/")
	h.waitFor("march.txt")
	h.waitGone("other.txt")
}
