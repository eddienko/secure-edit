package tui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// formDialog is a bordered box with several one-line fields. It is a Form, which
// is a Box underneath and so clears what is behind it; wrapping it in a Flex
// would not (see inputDialog). It adds a line of text at the bottom.
type formDialog struct {
	*tview.Form
	hint string // one or more lines
}

func (d *formDialog) Draw(screen tcell.Screen) {
	d.Form.Draw(screen)
	x, y, w, h := d.GetInnerRect()
	lines := strings.Split(d.hint, "\n")
	for i, l := range lines {
		if row := h - len(lines) + i; row >= 0 {
			drawText(screen, x, y+row, w, l, plain)
		}
	}
}

// formField describes one field of a fieldsDialog: a single line, or with Lines
// above 1 a box for several lines of text.
type formField struct {
	Label   string
	Initial string
	Lines   int
}

// textField is what both kinds of field have in common.
type textField interface {
	tview.FormItem
	GetText() string
}

// fieldsDialog asks for several pieces of text in one box. Tab and Shift-Tab
// move between the fields (and the up and down arrows between one-line fields),
// Ctrl-S confirms from anywhere, Enter confirms from a one-line field and
// starts a new line in a multi-line one, and Esc cancels. When it is confirmed,
// check looks at the values: if it returns an error the box stays open with the
// error on the status bar and the focus on the field it names; otherwise the box
// closes and done gets the values.
func (a *App) fieldsDialog(name, title, hint string, fieldSpecs []formField,
	check func(vals []string) (field int, err error), done func(vals []string, ok bool)) {

	form := tview.NewForm().SetItemPadding(0)
	form.SetFieldStyle(tcell.StyleDefault.Underline(true))
	form.SetLabelColor(tcell.ColorDefault)
	form.SetBorder(true).SetTitle(" "+title+" ").SetBorderPadding(0, 0, 1, 1)
	dlg := &formDialog{Form: form, hint: hint}

	fields := make([]textField, len(fieldSpecs))
	multi := make([]bool, len(fieldSpecs))
	rows := 0
	for i, f := range fieldSpecs {
		if f.Lines > 1 {
			form.AddTextArea(f.Label, "", 0, f.Lines, 0, nil)
			// With the cursor at the start: placed at the end, a long text
			// would be scrolled so that only its last line shows.
			form.GetFormItem(i).(*tview.TextArea).SetText(f.Initial, false)
			multi[i], rows = true, rows+f.Lines
		} else {
			form.AddInputField(f.Label, f.Initial, 0, nil, nil)
			rows++
		}
		fields[i] = form.GetFormItem(i).(textField)
	}
	values := func() []string {
		vals := make([]string, len(fields))
		for i, f := range fields {
			vals[i] = f.GetText()
		}
		return vals
	}
	focus := func(i int) {
		form.SetFocus((i + len(fields)) % len(fields))
		a.app.SetFocus(dlg)
	}
	current := func() int { i, _ := form.GetFocusedItemIndex(); return i }
	submit := func() {
		vals := values()
		if i, err := check(vals); err != nil {
			a.say(err.Error(), true)
			focus(i)
			return
		}
		a.closeDialog(name)
		done(vals, true)
	}

	for i := range fields {
		isMulti := multi[i]
		fields[i].(interface {
			SetInputCapture(func(*tcell.EventKey) *tcell.EventKey) *tview.Box
		}).SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
			switch ev.Key() {
			case tcell.KeyCtrlS:
				submit()
			case tcell.KeyEnter:
				if isMulti {
					return ev // a new line
				}
				submit()
			case tcell.KeyEscape:
				a.closeDialog(name)
				done(nil, false)
			case tcell.KeyDown, tcell.KeyUp:
				if isMulti {
					return ev // moves in the text
				}
				if ev.Key() == tcell.KeyDown {
					focus(current() + 1)
				} else {
					focus(current() - 1)
				}
			default:
				return ev
			}
			return nil
		})
	}
	a.openDialog(name, dlg, 78, rows+strings.Count(hint, "\n")+1+2)
}
