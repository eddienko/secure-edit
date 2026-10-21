package tui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// maxBuiltinEdit is the largest text the built-in editor opens; the text area
// gets slow well before the 1 MiB the preview shows.
const maxBuiltinEdit = 256 << 10

// builtinEditor is the text of the unlocked file being edited in the right pane
// instead of in an external editor (E). The text area keeps its own copies of the
// text, including its undo history, which can't be zeroed: closing the editor
// empties it, but see docs/security.md.
type builtinEditor struct {
	area  *numbered
	s     *Session
	dirty bool
	query string // what Ctrl-F last looked for
}

// editBuiltin opens the selected file in the built-in editor, unlocking it first.
func (a *App) editBuiltin() {
	a.unlockCurrent(func(s *Session) {
		switch {
		case s.Cipher == nil:
			a.say("the built-in editor is for files that exist; use e for a new one", true)
		case looksBinary(s.Plain):
			a.say("binary data can't be edited as text", true)
		case len(s.Plain) > maxBuiltinEdit:
			a.say(fmt.Sprintf("the text is over %s: use e to edit it in your editor", humanSize(maxBuiltinEdit)), true)
		default:
			a.startBuiltin(s)
		}
	})
}

func (a *App) startBuiltin(s *Session) {
	ta := &numbered{TextArea: tview.NewTextArea()}
	ta.SetText(string(s.Plain), false)
	ta.SetBorder(true)
	a.ed = &builtinEditor{area: ta, s: s}
	ta.SetChangedFunc(func() {
		a.ed.dirty = true
		a.editTitle()
	})
	a.body.RemoveItem(a.prev)
	a.body.AddItem(ta, 0, 2, true)
	a.app.SetFocus(ta)
	a.editTitle()
	a.renderStatus()
}

func (a *App) editTitle() {
	e := a.ed
	if e == nil {
		return
	}
	state := "(editing)"
	if e.dirty {
		state = "(editing, not saved)"
	}
	e.area.SetTitle(a.paneTitle(e.s.Meta().Title, filepath.Base(e.s.Path), state))
}

// editKey handles a key while the built-in editor is open; keys it doesn't use
// go on to the text area.
func (a *App) editKey(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyCtrlS:
		a.saveBuiltin()
		return nil
	case tcell.KeyCtrlT:
		on := a.ed.area.toggle()
		a.say(map[bool]string{true: "line numbers on; lines don't wrap while they show", false: "line numbers off"}[on], false)
		return nil
	case tcell.KeyCtrlF:
		a.findDialog()
		return nil
	case tcell.KeyF3:
		a.findNext()
		return nil
	case tcell.KeyCtrlG:
		a.gotoDialog()
		return nil
	case tcell.KeyCtrlA, tcell.KeyCtrlE, tcell.KeyHome, tcell.KeyEnd:
		lineEdge(a.ed.area.TextArea, ev.Key() == tcell.KeyCtrlA || ev.Key() == tcell.KeyHome)
		return nil
	case tcell.KeyEscape:
		if !a.ed.dirty {
			a.endBuiltin()
			return nil
		}
		a.choiceDialog("Close without saving?\n\nYour changes are lost.", []string{"Keep editing", "Save and close", "Discard"}, func(i int) {
			switch {
			case i == 1 && a.saveBuiltin():
				a.endBuiltin()
			case i == 2:
				a.endBuiltin()
			}
		})
		return nil
	}
	return ev
}

// saveBuiltin saves the text and reports whether that worked.
func (a *App) saveBuiltin() bool {
	e := a.ed
	saved, err := e.s.SaveText([]byte(e.area.GetText()))
	switch {
	case err != nil:
		a.say(err.Error(), true)
		return false
	case saved:
		a.say("saved "+filepath.Base(e.s.Path)+" (encrypted)", false)
	default:
		a.say("no changes to "+filepath.Base(e.s.Path), false)
	}
	e.dirty = false
	a.editTitle()
	return true
}

// endBuiltin closes the editor, dropping what is in the text area, and goes back
// to the preview of the file.
func (a *App) endBuiltin() {
	e := a.ed
	if e == nil {
		return
	}
	a.ed = nil
	e.area.SetChangedFunc(nil)
	e.area.SetText("", true)
	a.body.RemoveItem(e.area)
	a.body.AddItem(a.prev, 0, 2, false)
	a.app.SetFocus(a.tree)
	a.last = time.Now()
	if a.session != nil && !a.session.Locked() {
		a.showSession(a.session)
	}
	a.reload("")
}

// lineEdge moves the cursor to the start or the end of its line. The text area's
// own keys stop at the edge of a wrapped row, which on a long line is not where
// the line begins or ends.
func lineEdge(ta *tview.TextArea, start bool) {
	_, from, to := ta.GetSelection()
	all := ta.GetText()
	pos := to
	if start {
		pos = from
		pos = strings.LastIndexByte(all[:pos], '\n') + 1
	} else if i := strings.IndexByte(all[pos:], '\n'); i >= 0 {
		pos += i
	} else {
		pos = len(all)
	}
	ta.Select(pos, pos)
}

// findDialog asks what to look for and goes to the first match after the cursor.
func (a *App) findDialog() {
	e := a.ed
	a.inputDialog("Find", "Text:", e.query, func(q string, ok bool) {
		if !ok || q == "" {
			return
		}
		e.query = q
		a.findNext()
	})
}

// findNext selects the next match of the last search after the cursor, wrapping
// round to the start of the text. Case is ignored.
func (a *App) findNext() {
	e := a.ed
	if e.query == "" {
		a.findDialog()
		return
	}
	_, _, to := e.area.GetSelection()
	start, end, wrapped := find(e.area.GetText(), e.query, to)
	if start < 0 {
		a.say("not found: "+e.query, true)
		return
	}
	e.area.Select(start, end)
	if wrapped {
		a.say("search wrapped round to the start", false)
	}
}

// find looks for q in text from byte offset from, ignoring case, and wraps round
// once. It returns the match as byte offsets, or -1 if there is none.
func find(text, q string, from int) (start, end int, wrapped bool) {
	hay, needle := text, q
	if lt, lq := strings.ToLower(text), strings.ToLower(q); len(lt) == len(text) && len(lq) == len(q) {
		hay, needle = lt, lq // offsets still line up with the original text
	}
	if from > len(hay) {
		from = len(hay)
	}
	if i := strings.Index(hay[from:], needle); i >= 0 {
		return from + i, from + i + len(needle), false
	}
	if i := strings.Index(hay, needle); i >= 0 {
		return i, i + len(needle), true
	}
	return -1, -1, false
}

// gotoDialog asks for a line number and moves the cursor to the start of it.
func (a *App) gotoDialog() {
	e := a.ed
	text := e.area.GetText()
	lines := strings.Count(text, "\n") + 1
	a.inputDialog(fmt.Sprintf("Go to line (1-%d)", lines), "Line:", "", func(s string, ok bool) {
		if !ok || strings.TrimSpace(s) == "" {
			return
		}
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 1 {
			a.say("not a line number: "+s, true)
			return
		}
		pos := lineStart(text, n)
		e.area.Select(pos, pos)
	})
}

// lineStart is the byte offset of the start of line n (counting from 1); a line
// past the end gives the start of the last one.
func lineStart(text string, n int) int {
	pos := 0
	for ; n > 1; n-- {
		i := strings.IndexByte(text[pos:], '\n')
		if i < 0 {
			break
		}
		pos += i + 1
	}
	return pos
}

// numbered is the text area with, when on is set, the number of each line in a
// column on its left. The numbers count lines, not wrapped rows, so while they
// show the text doesn't wrap: a line is then one row, and long ones scroll
// sideways.
type numbered struct {
	*tview.TextArea
	on bool
}

// toggle switches the numbers and reports whether they are on.
func (n *numbered) toggle() bool {
	n.on = !n.on
	n.SetWrap(!n.on)
	return n.on
}

func (n *numbered) Draw(screen tcell.Screen) {
	lines := strings.Count(n.GetText(), "\n") + 1
	width := 0
	if n.on {
		width = len(strconv.Itoa(lines)) + 1
	}
	n.SetBorderPadding(0, 0, width, 0)
	n.TextArea.Draw(screen)
	if !n.on {
		return
	}
	x, y, _, height := n.GetRect()
	first, _ := n.GetOffset()
	cursor, _, _, _ := n.GetCursor()
	for row := 0; row < height-2 && first+row < lines; row++ {
		style := dim
		if first+row == cursor {
			style = bold
		}
		for i, r := range fmt.Sprintf("%*d", width-1, first+row+1) {
			screen.SetContent(x+1+i, y+1+row, r, nil, style)
		}
	}
}
