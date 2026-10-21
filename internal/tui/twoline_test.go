package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/eddienko/secure-edit/internal/vault"
)

// twoLineTree is a folder with titled and untitled files, a titled folder and a
// plain file.
func twoLineTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	k := folderLabelled(t, dir, "kubernetes", vault.Meta{Title: "Kubernetes"})
	labelledFile(t, k, "alertmanager.yaml", "pw", "x", vault.Meta{Title: "Alertmanager"})
	labelledFile(t, k, "kubeconfig-darkroom", "pw", "x", vault.Meta{Title: "Darkroom Admin Kubeconfig"})
	seditFile(t, k, "notes.txt", "pw", "x")
	os.Mkdir(filepath.Join(dir, "work"), 0o700)
	labelledFile(t, dir, "fake.yaml", "pw", "x", vault.Meta{Title: "Fake secret"})
	plainFile(t, dir, "readme.md", "x")
	return dir
}

// leftRows are the rows of the list, the left 36 columns of the screen.
func (h *harness) leftRows() []string {
	var out []string
	for _, r := range strings.Split(h.screen(), "\n") {
		runes := []rune(r)
		if len(runes) > 36 {
			runes = runes[:36]
		}
		out = append(out, string(runes))
	}
	return out
}

func (h *harness) leftRow(containing string) (index int, text string) {
	h.t.Helper()
	for i, r := range h.leftRows() {
		if strings.Contains(r, containing) {
			return i, r
		}
	}
	h.t.Fatalf("no row in the list contains %q:\n%s", containing, h.screen())
	return
}

func (h *harness) openFolder(name string) {
	h.selectFile(name)
	h.key(tcell.KeyRight)
}

func TestTheListStartsWithTitlesOnlyAndVShowsTheNamesUnderThem(t *testing.T) {
	h := newHarness(t, twoLineTree(t), Options{})
	h.openFolder("kubernetes/")
	h.waitFor("▪ Alertmanager")
	h.notOnScreen("alertmanager.yaml") // titles only: no name, beside or under
	h.press('v')
	h.waitFor("shows its name under its title")
	h.waitFor("alertmanager.yaml")
	i, _ := h.leftRow("▪ Alertmanager")
	if name := h.leftRows()[i+1]; !strings.Contains(name, "alertmanager.yaml") {
		t.Errorf("the name should be on the row under the title:\n%s", h.screen())
	}
	h.press('v')
	h.waitFor("titles only")
	h.waitGone("alertmanager.yaml")
}

func TestTwoLineListOnlyTitledFilesGetASecondRow(t *testing.T) {
	h := newHarness(t, twoLineTree(t), Options{TwoLine: true})
	h.openFolder("kubernetes/")
	h.waitFor("▪ notes.txt")
	rows := h.leftRows()
	count := func(s string) int {
		n := 0
		for _, r := range rows {
			if strings.Contains(r, s) {
				n++
			}
		}
		return n
	}
	// notes.txt has no title: its name is its row, once.
	if count("notes.txt") != 1 || count("readme.md") != 1 {
		t.Errorf("an untitled file is listed on one row:\n%s", strings.Join(rows, "\n"))
	}
	// A titled folder is listed by its title in either mode.
	if i, row := h.leftRow("Kubernetes"); strings.Contains(row, "kubernetes/") {
		t.Errorf("row %d: %q", i, row)
	}
	// The name is under the title, starting in the same column.
	i, title := h.leftRow("Darkroom Admin Kubeconfig")
	name := h.leftRows()[i+1]
	if !strings.Contains(name, "kubeconfig-darkroom") {
		t.Fatalf("title %q, name %q", title, name)
	}
	if ti, ni := len([]rune(title[:strings.Index(title, "Darkroom")])), len([]rune(name[:strings.Index(name, "kubeconfig-darkroom")])); ti != ni {
		t.Errorf("the name starts in column %d, the title in %d:\n%s\n%s", ni, ti, title, name)
	}
}

func TestTwoLineGuidesRunThroughTheNameRows(t *testing.T) {
	h := newHarness(t, twoLineTree(t), Options{TwoLine: true})
	h.openFolder("kubernetes/")
	h.waitFor("▪ Alertmanager")
	h.settle()
	i, _ := h.leftRow("▪ Alertmanager")
	rows := h.leftRows()
	// Alertmanager is not the last in its folder: the guide continues on its name row.
	if !strings.Contains(rows[i+1], "│") {
		t.Errorf("no guide on the name row of a file that isn't last:\n%s", strings.Join(rows[i:i+3], "\n"))
	}
	// The folder's own line continues down past its children to the next entry.
	_, work := h.leftRow("work/")
	if !strings.Contains(work, "├─") {
		t.Errorf("work/ is not last, so it has a branch: %q", work)
	}
	// The last entry of a folder is closed with a corner.
	_, last := h.leftRow("readme.md")
	if !strings.Contains(last, "└─") {
		t.Errorf("the last entry has no corner: %q", last)
	}
	// And a titled last file has no guide on its name row.
	dir := t.TempDir()
	labelledFile(t, dir, "only.txt", "pw", "x", vault.Meta{Title: "Only one"})
	h2 := newHarness(t, dir, Options{TwoLine: true})
	h2.waitFor("└─ ▪ Only one")
	j, _ := h2.leftRow("Only one")
	if name := h2.leftRows()[j+1]; strings.Contains(name, "│") || !strings.Contains(name, "only.txt") {
		t.Errorf("the name row of the last file: %q", name)
	}
}

func TestTwoLineSelectionCoversOnlyTheTitleRowAndNotTheGuides(t *testing.T) {
	h := newHarness(t, twoLineTree(t), Options{TwoLine: true})
	h.openFolder("kubernetes/")
	h.selectFile("kubeconfig-darkroom")
	h.settle()
	reversed := func(containing string) string {
		for _, row := range h.grid() {
			var line, rev strings.Builder
			for _, c := range row[:36] {
				line.WriteRune(c.r)
				if c.attr&tcell.AttrReverse != 0 {
					rev.WriteRune('R')
				} else {
					rev.WriteRune('.')
				}
			}
			if strings.Contains(line.String(), containing) {
				return rev.String() + "|" + line.String()
			}
		}
		t.Fatalf("no row with %q", containing)
		return ""
	}
	title := reversed("Darkroom Admin")
	if !strings.Contains(title, "R") || strings.Contains(strings.SplitN(title, "|", 2)[0][:6], "R") {
		t.Errorf("the title row: %s", title)
	}
	// The name row under it is not highlighted: only the title row is.
	if name := reversed("kubeconfig-darkroom"); strings.Contains(strings.SplitN(name, "|", 2)[0], "R") {
		t.Errorf("the name row is highlighted: %s", name)
	}
	// Another file's rows are not highlighted.
	if other := reversed("alertmanager.yaml"); strings.Contains(strings.SplitN(other, "|", 2)[0], "R") {
		t.Errorf("an unselected name row is highlighted: %s", other)
	}
	h.onlyTerminalColours("the two-line list")
}

func TestTheArrowKeysSkipTheNameRows(t *testing.T) {
	h := newHarness(t, twoLineTree(t), Options{TwoLine: true})
	h.openFolder("kubernetes/")
	h.selectFile("alertmanager.yaml")
	var seen []string
	for i := 0; i < 3; i++ {
		h.key(tcell.KeyDown)
		seen = append(seen, h.selected())
	}
	if got := strings.Join(seen, " "); got != "kubeconfig-darkroom notes.txt work" {
		t.Errorf("Down went through %q, want each file once", got)
	}
	for i := 0; i < 3; i++ {
		h.key(tcell.KeyUp)
	}
	if got := h.selected(); got != "alertmanager.yaml" {
		t.Errorf("Up came back to %q", got)
	}
}

func TestTwoLineHomeAndEndSelectRealEntries(t *testing.T) {
	dir := t.TempDir()
	labelledFile(t, dir, "last.txt", "pw", "x", vault.Meta{Title: "Titled and last"})
	h := newHarness(t, dir, Options{TwoLine: true})
	h.waitFor("Titled and last")
	h.key(tcell.KeyEnd)
	if got := h.selected(); got != "last.txt" {
		t.Errorf("End selected %q: it must not land on the name row", got)
	}
	h.press('G')
	h.key(tcell.KeyHome)
	if got := h.selected(); got != filepath.Base(dir) {
		t.Errorf("Home selected %q", got)
	}
}

func TestTwoLineFoldersOpenCloseAndLeftGoesToTheFolder(t *testing.T) {
	h := newHarness(t, twoLineTree(t), Options{TwoLine: true})
	h.selectFile("kubernetes/")
	h.notOnScreen("alertmanager.yaml")
	h.key(tcell.KeyRight)
	h.waitFor("alertmanager.yaml")
	h.selectFile("alertmanager.yaml")
	h.key(tcell.KeyLeft) // on a file: to its folder
	if got := h.selected(); got != "kubernetes" {
		t.Errorf("Left selected %q", got)
	}
	h.key(tcell.KeyLeft) // on an open folder: close it
	h.waitGone("alertmanager.yaml")
}

func TestTwoLineEnterUnlocksAndTheNameRowDoesNotGetInTheWay(t *testing.T) {
	dir := t.TempDir()
	p := labelledFile(t, dir, "vpn.txt", "pw", secretText, vault.Meta{Title: "Office VPN"})
	h := newHarness(t, dir, Options{TwoLine: true})
	h.remember(p)
	h.selectFile("vpn.txt")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	h.waitFor("Office VPN · vpn.txt  (unlocked")
}

func TestTwoLineSurvivesTheFilterAndRefresh(t *testing.T) {
	dir := twoLineTree(t)
	h := newHarness(t, dir, Options{TwoLine: true})
	h.typeFilter("dark")
	h.waitFor("1 match")
	h.waitFor("▪ Darkroom Admin Kubeconfig")
	h.waitFor("kubeconfig-darkroom") // its name row
	h.notOnScreen("alertmanager")
	h.key(tcell.KeyEnter)
	h.key(tcell.KeyEscape)
	h.waitFor("▪ Fake secret")
	h.press('R')
	h.waitFor("fake.yaml")
	if got := h.selected(); got == "" {
		t.Error("nothing is selected after a refresh")
	}
}

func TestTwoLineTitlesStayEscapedAndSafe(t *testing.T) {
	dir := t.TempDir()
	labelledFile(t, dir, "a.txt", "pw", "x", vault.Meta{Title: "[red]Bold[-] [::b]x"})
	seditFile(t, dir, "[odd].txt", "pw", "x")
	h := newHarness(t, dir, Options{TwoLine: true})
	h.waitFor("▪ [red]Bold[-] [::b]x")
	h.waitFor("a.txt")
	h.waitFor("▪ [odd].txt")
	h.onlyTerminalColours("escaped titles in the two-line list")
}

func TestVIsInTheHelp(t *testing.T) {
	h := newHarness(t, t.TempDir(), Options{})
	h.press('?')
	h.waitFor("name under its title, or only the title")
}

func TestTwoLineChangeOfTheSelectionLocksAsBefore(t *testing.T) {
	dir := t.TempDir()
	p := labelledFile(t, dir, "a.txt", "pw", secretText, vault.Meta{Title: "First"})
	labelledFile(t, dir, "b.txt", "pw", "x", vault.Meta{Title: "Second"})
	h := newHarness(t, dir, Options{TwoLine: true})
	h.remember(p)
	h.selectFile("a.txt")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	h.key(tcell.KeyDown)
	h.waitGone(secretText)
	if h.session() != nil {
		t.Error("moving to another file left the first one unlocked")
	}
}
