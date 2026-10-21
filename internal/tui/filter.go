package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
)

// The list can be narrowed with /: type a few words and only the files and
// folders whose title or name contain all of them (in any case), what is inside
// a matching folder, and the folders that lead to a match, are listed. The folders on the way are opened for you, so a match deep in
// the tree is not hidden behind a closed folder.

const (
	maxIndexed   = 5000 // entries looked at when filtering, so a huge tree can't hang the interface
	maxIndexDeep = 10   // folders deep
	maxFilterLen = 80   // characters
)

// walk lists everything under root, in the order the list shows it.
func walk(root string) []Entry {
	var out []Entry
	var rec func(dir string, depth int)
	rec = func(dir string, depth int) {
		entries, err := List(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if len(out) >= maxIndexed {
				return
			}
			out = append(out, e)
			if e.Kind == KindDir && depth < maxIndexDeep {
				rec(e.Path, depth+1)
			}
		}
	}
	rec(root, 0)
	return out
}

// matches reports whether every word is in the entry's title or name.
func (e Entry) matches(words []string) bool {
	hay := strings.ToLower(e.Meta.Title + " " + e.Name)
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// filtering reports whether the list is narrowed.
func (a *App) filtering() bool { return a.filter != "" }

// computeVisible works out which paths the narrowed list shows: the matches and
// every folder on the way to one. It reads the folders only when the index is
// out of date, so typing is cheap.
func (a *App) computeVisible() {
	a.visible, a.matchCount = nil, 0
	words := strings.Fields(strings.ToLower(a.filter))
	if len(words) == 0 {
		return
	}
	if a.index == nil {
		a.index = walk(a.root)
	}
	a.visible = map[string]bool{}
	var matchedDirs []string
	for _, e := range a.index {
		if !e.matches(words) {
			continue
		}
		a.matchCount++
		a.visible[e.Path] = true
		if e.Kind == KindDir {
			matchedDirs = append(matchedDirs, e.Path+string(filepath.Separator))
		}
		for p := filepath.Dir(e.Path); p != a.root && strings.HasPrefix(p, a.root); p = filepath.Dir(p) {
			a.visible[p] = true
		}
	}
	// A folder that matches shows everything in it: someone looking for "bank"
	// wants the whole Banking folder, not only what has "bank" in its name.
	for _, e := range a.index {
		for _, d := range matchedDirs {
			if strings.HasPrefix(e.Path, d) {
				a.visible[e.Path] = true
				break
			}
		}
	}
}

// startFilter begins (or goes on) typing the filter.
func (a *App) startFilter() {
	a.filterEditing = true
	a.renderStatus()
}

// setFilter changes the text of the filter and redraws the list.
func (a *App) setFilter(text string) {
	a.filter = text
	a.rebuild("")
}

// clearFilter removes the filter and shows the whole list again.
func (a *App) clearFilter() {
	a.filterEditing = false
	if a.filter == "" {
		a.renderStatus()
		return
	}
	a.filter, a.visible, a.index, a.matchCount = "", nil, nil, 0
	a.rebuild("")
	a.renderStatus()
}

// filterKey handles a key while the filter is being typed. Enter keeps the
// filter and goes back to the list; Esc removes it; the keys that move the
// selection still do, so a match can be picked while typing.
func (a *App) filterKey(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyCtrlC:
		return ev
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn:
		return ev
	case tcell.KeyHome:
		a.selectEdge(true)
	case tcell.KeyEnd:
		a.selectEdge(false)
	case tcell.KeyEnter:
		a.filterEditing = false
		a.renderStatus()
	case tcell.KeyEscape:
		a.clearFilter()
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if _, size := utf8.DecodeLastRuneInString(a.filter); size > 0 {
			a.setFilter(a.filter[:len(a.filter)-size])
		}
	case tcell.KeyCtrlU:
		a.setFilter("")
	case tcell.KeyRune:
		if utf8.RuneCountInString(a.filter) < maxFilterLen {
			a.setFilter(a.filter + string(ev.Rune()))
		}
	}
	return nil
}

// filterLine is what the bar shows while the filter is typed.
func (a *App) filterLine() string {
	count := "type to filter by title or name"
	switch {
	case a.filter != "" && a.matchCount == 0:
		count = "no match"
	case a.filter != "":
		count = plural(a.matchCount, "match", "matches")
	}
	return "/" + a.filter + "▏  " + count + "   Enter: keep   Esc: clear"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
