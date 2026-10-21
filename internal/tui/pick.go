package tui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// pickItem is one choice in a pickDialog.
type pickItem struct {
	Label  string // what it is called, e.g. a folder's path
	Detail string // more to read, e.g. its title; searched too
}

// pickDialog asks for one of items from a list. Typing narrows the list to the
// items whose label or detail contain every word typed, the arrow keys move,
// Enter picks and Esc cancels. done gets the index into items.
//
// The list is itself the bordered box (a Box clears what is under it; a Flex
// around it would not), as with inputDialog.
func (a *App) pickDialog(name, title string, items []pickItem, done func(index int, ok bool)) {
	list := tview.NewList().ShowSecondaryText(false).SetHighlightFullLine(true)
	list.SetMainTextStyle(plain).SetSelectedStyle(invert)
	list.SetBorder(true).SetBorderPadding(0, 0, 1, 1)

	query := ""
	var shown []int // indexes into items, for the rows in the list
	refill := func() {
		words := strings.Fields(strings.ToLower(query))
		list.Clear()
		shown = shown[:0]
	next:
		for i, it := range items {
			hay := strings.ToLower(it.Label + " " + it.Detail)
			for _, w := range words {
				if !strings.Contains(hay, w) {
					continue next
				}
			}
			text := tview.Escape(safeText(it.Label, false))
			if d := safeText(it.Detail, false); d != "" {
				text += "   " + tview.Escape(d)
			}
			list.AddItem(text, "", 0, nil)
			shown = append(shown, i)
		}
		t := " " + title + "  (type to narrow, Enter: pick, Esc: cancel) "
		if query != "" {
			t = " " + title + "  /" + query + "▏ "
		}
		list.SetTitle(t)
	}
	refill()

	list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyEnter:
			i := list.GetCurrentItem()
			if i < 0 || i >= len(shown) {
				return nil // nothing to pick
			}
			a.closeDialog(name)
			done(shown[i], true)
		case tcell.KeyEscape:
			a.closeDialog(name)
			done(-1, false)
		case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn, tcell.KeyHome, tcell.KeyEnd:
			return ev
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if query != "" {
				r := []rune(query)
				query = string(r[:len(r)-1])
				refill()
			}
		case tcell.KeyCtrlU:
			query = ""
			refill()
		case tcell.KeyRune:
			if len([]rune(query)) < maxFilterLen {
				query += string(ev.Rune())
				refill()
			}
		case tcell.KeyCtrlC:
			return ev
		}
		return nil
	})
	height := len(items) + 2
	if height > 16 {
		height = 16
	}
	a.openDialog(name, list, 72, height)
}
