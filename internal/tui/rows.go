package tui

import (
	"github.com/rivo/tview"
)

// The two-line list. A file with a title is listed on two rows: its title, and
// under it its name in dim text. The tree widget draws one row per node, so the
// name is a second node under the title's, which can't be selected (the arrow
// keys skip it), and the guide lines are drawn here, as text, because the
// widget's own would join the name row to the tree as if it were another file.
// The widget gives the selected node reverse video, which would also cover the
// guide lines in the text; so reverse video is turned on by a tag in the text of
// the selected node instead, and the text is swapped when the selection moves
// (paintSelection). Only the title row is highlighted; the name row under it
// stays as it is.

// rowText is the text of a row in each of its two states.
type rowText struct {
	normal, selected string
	comp             *tview.TreeNode // the name row under a titled file, if any
}

// styleRow gives the node child, an entry of the folder node parent, its guide
// lines and, if it is a file with a title, its second row.
func (a *App) styleRow(parent, child *tview.TreeNode, e Entry, last bool) {
	pc := a.cont[parent]
	branch, below := "├─ ", "│  "
	if last {
		branch, below = "└─ ", "   "
	}
	a.cont[child] = pc + below // the guide lines for what is inside a folder

	text, _ := a.label(e)
	title := safeText(e.Meta.Title, false)
	if e.Kind == KindSedit && title != "" {
		text = "▪ " + tview.Escape(title) // the name goes on the next row
	}
	r := &rowText{normal: pc + branch + text, selected: pc + branch + "[::r]" + text}
	if e.Kind == KindSedit && title != "" {
		name := tview.Escape(safeText(e.Name, false))
		pad := pc + below + "  " // under the title, after the "▪ "
		r.comp = tview.NewTreeNode(pad + "[::d]" + name).SetSelectable(false).SetTextStyle(plain)
	}
	child.SetText(r.normal)
	a.rows[child] = r
}

// paintSelection makes the selected row reversed, and the row that was selected
// before plain again.
func (a *App) paintSelection() {
	if !a.twoLine {
		return
	}
	cur := a.tree.GetCurrentNode()
	if a.painted != nil && a.painted != cur {
		a.paint(a.painted, false)
	}
	if cur != nil {
		a.paint(cur, true)
	}
	a.painted = cur
}

func (a *App) paint(n *tview.TreeNode, selected bool) {
	r := a.rows[n]
	if r == nil {
		return
	}
	if selected {
		n.SetText(r.selected)
	} else {
		n.SetText(r.normal)
	}
}

// toggleLayout switches between the one-line and the two-line list.
func (a *App) toggleLayout() {
	a.twoLine = !a.twoLine
	a.tree.SetGraphics(!a.twoLine).SetAlign(a.twoLine) // see the top of this file: the guides are ours
	a.rebuild("")
	if a.twoLine {
		a.say("two-line list: a titled file shows its name under the title (v switches back)", false)
	} else {
		a.say("one-line list (v switches to two lines)", false)
	}
}
