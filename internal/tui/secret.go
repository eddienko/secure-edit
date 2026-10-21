package tui

import (
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/eddienko/secure-edit/internal/vault"
)

// secretBox is a password dialog. What is typed lives in a byte slice that is
// zeroed when it is discarded; a tview.InputField would keep it in an immutable
// string that can't be wiped.
type secretBox struct {
	*tview.Box
	label string
	err   string // shown under the field, e.g. "Wrong password"
	value []byte
	done  func(pw []byte, ok bool)
}

func newSecretBox(title, label, err string, done func(pw []byte, ok bool)) *secretBox {
	s := &secretBox{Box: tview.NewBox(), label: label, err: err, done: done}
	s.SetBorder(true).SetTitle(" " + title + " ")
	return s
}

func (s *secretBox) Draw(screen tcell.Screen) {
	s.DrawForSubclass(screen, s)
	x, y, w, h := s.GetInnerRect()
	if w < 4 || h < 3 {
		return
	}
	tview.Print(screen, s.label, x+1, y, w-2, tview.AlignLeft, tcell.ColorWhite)

	// The field: one dot per character typed, never the characters.
	n := utf8.RuneCount(s.value)
	if n > w-5 {
		n = w - 5 // long passwords scroll out of sight; the count is not a secret
	}
	field := "> "
	for i := 0; i < n; i++ {
		field += "•"
	}
	tview.Print(screen, field, x+1, y+1, w-2, tview.AlignLeft, tcell.ColorYellow)
	screen.SetContent(x+1+2+n, y+1, '█', nil, tcell.StyleDefault.Foreground(tcell.ColorYellow))

	if s.err != "" {
		tview.Print(screen, s.err, x+1, y+2, w-2, tview.AlignLeft, tcell.ColorRed)
	} else if h >= 4 {
		tview.Print(screen, "Enter to confirm, Esc to cancel", x+1, y+3, w-2, tview.AlignLeft, tcell.ColorGray)
	}
}

func (s *secretBox) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return s.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEnter:
			pw := s.value
			s.value = nil // ownership moves to the callback
			s.done(pw, true)
		case tcell.KeyEscape, tcell.KeyCtrlC:
			s.Wipe()
			s.done(nil, false)
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if len(s.value) > 0 {
				_, size := utf8.DecodeLastRune(s.value)
				vault.Wipe(s.value[len(s.value)-size:])
				s.value = s.value[:len(s.value)-size]
				s.err = ""
			}
		case tcell.KeyCtrlU:
			s.Wipe()
			s.err = ""
		case tcell.KeyRune:
			s.value = utf8.AppendRune(s.value, event.Rune())
			s.err = ""
		}
	})
}

// Wipe zeroes what has been typed so far.
func (s *secretBox) Wipe() {
	vault.Wipe(s.value)
	s.value = nil
}
