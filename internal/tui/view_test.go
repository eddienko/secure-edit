package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/eddienko/secure-edit/internal/vault"
)

// lastRow is the hint bar.
func (h *harness) bar() string {
	var last string
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		rows := strings.Split(h.screen(), "\n")
		last = strings.TrimSpace(rows[len(rows)-1])
		if strings.Contains(last, "quit") {
			break
		}
	}
	return last
}

func (h *harness) resize(w, ht int) {
	h.sim.SetSize(w, ht)
	time.Sleep(150 * time.Millisecond) // for the resize to be handled and drawn
	h.settle()
}

func longText(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestHelpAndQuitAreAlwaysOnTheBar(t *testing.T) {
	dir := t.TempDir()
	seditFile(t, dir, "notes.txt", "pw", "x")
	h := newHarness(t, dir, Options{TickEvery: 50 * time.Millisecond}) // redraws promptly after a resize
	h.selectFile("notes.txt")
	for _, w := range []int{110, 80, 60, 44, 30} {
		h.resize(w, 24)
		if bar := h.bar(); !strings.Contains(bar, "? help  q quit") {
			t.Errorf("width %d: the bar is %q", w, bar)
		}
	}
	// Wide enough, everything fits; narrow, the least useful go first.
	h.resize(120, 24)
	if bar := h.bar(); !strings.Contains(bar, "p password") {
		t.Errorf("a wide bar should have every hint: %q", bar)
	}
	h.resize(60, 24)
	bar := h.bar()
	if !strings.Contains(bar, "↑↓ move") || strings.Contains(bar, "p password") {
		t.Errorf("a narrow bar should keep the first hints and drop the last: %q", bar)
	}
}

func TestHintsFollowWhatIsSelected(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "sub"), 0o700)
	path := seditFile(t, dir, "notes.txt", "pw", longText(80))
	plainFile(t, dir, "readme.md", "plain")
	h := newHarness(t, dir, Options{})
	h.remember(path)

	h.selectFile("sub/")
	h.waitFor("→ open")
	h.notOnScreen("⏎ unlock")

	h.selectFile("readme.md")
	h.waitFor("R refresh")
	h.notOnScreen("⏎ unlock")
	h.notOnScreen("y copy")

	h.selectFile("notes.txt")
	h.waitFor("⏎ unlock")
	h.notOnScreen("l lock")

	h.key(tcell.KeyEnter)
	h.waitFor("line 1")
	h.waitFor("J/K scroll")
	h.waitFor("l lock")
	h.notOnScreen("⏎ unlock")

	h.key(tcell.KeyTab)
	h.waitFor("PgUp/PgDn page")
	h.waitFor("Esc back")
	h.key(tcell.KeyEscape)
	h.waitFor("J/K scroll")
}

func TestTheListIsSortedByTheTitleShown(t *testing.T) {
	dir := t.TempDir()
	labelledFile(t, dir, "zzz.txt", "pw", "x", vault.Meta{Title: "Alpha"})
	seditFile(t, dir, "mmm.txt", "pw", "x")
	labelledFile(t, dir, "aaa.txt", "pw", "x", vault.Meta{Title: "Zulu"})
	es, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range es {
		got = append(got, e.Name)
	}
	if want := "zzz.txt mmm.txt aaa.txt"; strings.Join(got, " ") != want {
		t.Errorf("order %v, want %s (Alpha, mmm.txt, Zulu)", got, want)
	}
	// Equal keys fall back to the file name.
	labelledFile(t, dir, "b.txt", "pw", "x", vault.Meta{Title: "same"})
	labelledFile(t, dir, "a.txt", "pw", "x", vault.Meta{Title: "Same"})
	es, _ = List(dir)
	var pair []string
	for _, e := range es {
		if strings.EqualFold(e.Meta.Title, "same") {
			pair = append(pair, e.Name)
		}
	}
	if strings.Join(pair, " ") != "a.txt b.txt" {
		t.Errorf("tie order %v", pair)
	}
}

func TestTheUnlockedPaneNamesTheTitleAndKeepsTheDescription(t *testing.T) {
	dir := t.TempDir()
	path := labelledFile(t, dir, "vpn.txt", "pw", "host=vpn.example.com\nuser=admin\n",
		vault.Meta{Title: "Work VPN login", Description: "Office VPN.\nRotate every 90 days."})
	h := newHarness(t, dir, Options{})
	h.remember(path)
	h.selectFile("vpn.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("host=vpn.example.com")
	h.waitFor("Work VPN login · vpn.txt  (unlocked, locks in")
	h.waitFor("Office VPN.")
	h.waitFor("Rotate every 90 days.")
	scr := h.screen()
	if !strings.Contains(scr, "│───────") {
		t.Errorf("no rule between the description and the text:\n%s", scr)
	}
	// The description comes before the text.
	if strings.Index(scr, "Rotate every 90 days.") > strings.Index(scr, "host=vpn.example.com") {
		t.Errorf("the description should be above the text:\n%s", scr)
	}
	h.onlyTerminalColours("an unlocked file with a description")
}

func TestAFileWithoutLabelsKeepsTheOldPaneTitle(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "notes.txt", "pw", secretText)
	h := newHarness(t, dir, Options{})
	h.remember(path)
	h.selectFile("notes.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("notes.txt  (unlocked, locks in")
	h.notOnScreen("·  notes.txt")
	h.notOnScreen("│───────") // and no rule under a description: there is none
}

func TestALongTitleIsCutShortInThePaneTitleButTheNameStays(t *testing.T) {
	dir := t.TempDir()
	path := labelledFile(t, dir, "vpn.txt", "pw", secretText,
		vault.Meta{Title: strings.Repeat("Very long title ", 10)})
	h := newHarness(t, dir, Options{TickEvery: 50 * time.Millisecond})
	h.remember(path)
	h.resize(80, 24)
	h.waitFor("Very long")
	h.key(tcell.KeyDown) // the list shows the title, not the name
	h.key(tcell.KeyEnter)
	h.waitFor("… · vpn.txt  (unlocked, locks in")
}

func TestALongDescriptionIsCutAtThreeRowsWithADots(t *testing.T) {
	dir := t.TempDir()
	path := labelledFile(t, dir, "a.txt", "pw", secretText,
		vault.Meta{Description: "one\ntwo\nthree\nfour\nfive"})
	h := newHarness(t, dir, Options{})
	h.remember(path)
	h.selectFile("a.txt")
	h.key(tcell.KeyEnter)
	h.waitFor(secretText)
	h.waitFor("three")
	h.notOnScreen("four")
	h.waitFor("…")
}

func TestScrollingFromTheList(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "long.txt", "pw", longText(120))
	seditFile(t, dir, "other.txt", "pw", "x")
	h := newHarness(t, dir, Options{})
	h.remember(path)
	h.selectFile("long.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("line 1")
	h.waitFor("lines 1-")
	h.waitFor("of 120")

	cur := func() string {
		var n string
		h.do(func() { e, _ := h.a.current(); n = e.Name })
		return n
	}
	h.key(tcell.KeyPgDn) // pages the text, and the selection stays
	h.waitGone("line 1\n")
	h.settle()
	if cur() != "long.txt" {
		t.Errorf("PgDn moved the selection to %s", cur())
	}
	var off int
	h.do(func() { off = h.a.prev.offset })
	if off < 20 {
		t.Errorf("PgDn scrolled only %d rows", off)
	}
	h.key(tcell.KeyPgUp)
	h.waitFor("lines 1-")

	h.press('J')
	h.press('J')
	h.press('J')
	h.settle()
	h.do(func() { off = h.a.prev.offset })
	if off != 3 {
		t.Errorf("three J presses scrolled %d rows", off)
	}
	h.press('K')
	h.settle()
	h.do(func() { off = h.a.prev.offset })
	if off != 2 {
		t.Errorf("K after three Js left the offset at %d", off)
	}
	h.press(' ')
	h.settle()
	h.do(func() { off = h.a.prev.offset })
	if off < 20 {
		t.Errorf("Space scrolled only %d rows", off)
	}
	if cur() != "long.txt" {
		t.Errorf("scrolling moved the selection to %s", cur())
	}
	h.key(tcell.KeyEnd) // End still moves the selection, as before
	h.waitFor("other.txt")
}

func TestPagingKeysMoveTheListWhenNoTextIsShown(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 12; i++ {
		seditFile(t, dir, fmt.Sprintf("f%02d.txt", i), "pw", "x")
	}
	h := newHarness(t, dir, Options{})
	h.selectFile("f00.txt")
	h.key(tcell.KeyPgDn)
	h.settle()
	var name string
	h.do(func() { e, _ := h.a.current(); name = e.Name })
	if name == "f00.txt" {
		t.Error("PgDn on a locked file did nothing; it should page the list")
	}
}

func TestNoPositionMarkerWhenEverythingFits(t *testing.T) {
	dir := t.TempDir()
	path := seditFile(t, dir, "short.txt", "pw", "a\nb\n")
	h := newHarness(t, dir, Options{})
	h.remember(path)
	h.selectFile("short.txt")
	h.key(tcell.KeyEnter)
	h.waitFor("a")
	h.notOnScreen("lines 1-")
}

func TestWords(t *testing.T) {
	for d, want := range map[time.Duration]string{
		time.Second:                 "1 second",
		30 * time.Second:            "30 seconds",
		time.Minute:                 "1 minute",
		2 * time.Minute:             "2 minutes",
		90 * time.Second:            "1 minute 30 seconds",
		5*time.Minute + time.Second: "5 minutes 1 second",
	} {
		if got := words(d); got != want {
			t.Errorf("words(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestHelpSaysTheIdleTimeInWordsAndMentionsScrolling(t *testing.T) {
	h := newHarness(t, t.TempDir(), Options{})
	h.press('?')
	h.waitFor("2 minutes without a key press")
	h.waitFor("PgUp PgDn")
	h.notOnScreen("2m0s")
}

func TestTheLockedPaneTitleNamesTheTitleToo(t *testing.T) {
	dir := t.TempDir()
	labelledFile(t, dir, "vpn.txt", "pw", secretText, vault.Meta{Title: "Work VPN login"})
	seditFile(t, dir, "notes.txt", "pw", secretText)
	h := newHarness(t, dir, Options{TickEvery: 50 * time.Millisecond})
	h.selectFile("vpn.txt")
	h.waitFor("Work VPN login · vpn.txt  (locked)")
	h.selectFile("notes.txt") // no title: the name alone, as before
	h.waitFor(" notes.txt  (locked) ")
	h.notOnScreen("· notes.txt")

	// Cut to fit, with the file name and the state kept.
	long := t.TempDir()
	labelledFile(t, long, "vpn.txt", "pw", secretText, vault.Meta{Title: strings.Repeat("Very long title ", 10)})
	h2 := newHarness(t, long, Options{TickEvery: 50 * time.Millisecond})
	h2.resize(80, 24)
	h2.waitFor("Very long")
	h2.key(tcell.KeyDown)
	h2.waitFor("… · vpn.txt  (locked)")
}
