package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"

	"github.com/eddienko/secure-edit/internal/vault"
)

// maxPreview is how much of a file is shown; the rest is cut off with a note.
const maxPreview = 1 << 20

// preview is the right-hand pane. It shows a message, or the decrypted text of
// the unlocked file. The text is drawn straight from the session's own bytes, so
// that zeroing them when the file is locked really does remove them; nothing is
// copied into the UI library's buffers.
type preview struct {
	*tview.Box
	message []line // shown when text is nil
	text    []byte // not owned: it is the session's Plain
	offset  int    // first visible row
	rows    int    // total rows at the last width
	width   int
	height  int
}

// line is a paragraph of a message: it is wrapped to the width, and may have
// line breaks in it.
type line struct {
	text  string
	style tcell.Style
}

func newPreview() *preview {
	p := &preview{Box: tview.NewBox()}
	p.SetBorder(true)
	return p
}

// showMessage replaces the pane's content with a message. Its first line that
// isn't empty is the headline.
func (p *preview) showMessage(msg string) {
	var lines []line
	headline := true
	for _, l := range strings.Split(msg, "\n") {
		style := plain
		if headline && l != "" {
			style, headline = bold, false
		}
		lines = append(lines, line{l, style})
	}
	p.text, p.message, p.offset = nil, lines, 0
}

// showLocked shows what a locked sedit file says about itself: its title and
// description if it has them, and that it is locked.
func (p *preview) showLocked(m vault.Meta) {
	if m.IsZero() {
		p.showMessage("\nLOCKED\n\nPress Enter to unlock.\nPress e to edit.")
		return
	}
	lines := []line{{"", plain}}
	if m.Title != "" {
		lines = append(lines, line{safeText(m.Title, false), bold}, line{"", plain})
	}
	if m.Description != "" {
		lines = append(lines, line{safeText(m.Description, true), plain}, line{"", plain})
	}
	lines = append(lines,
		line{"LOCKED", bold}, line{"", plain},
		line{"Press Enter to unlock.", plain}, line{"Press e to edit.", plain})
	p.text, p.message, p.offset = nil, lines, 0
}

// showText shows decrypted text. The slice is read at every redraw, not copied.
func (p *preview) showText(text []byte) {
	p.text, p.message, p.offset = text, nil, 0
}

// looksBinary reports whether data should be treated as binary rather than text.
func looksBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	head := data[:n]
	for _, b := range head {
		if b == 0 {
			return true
		}
	}
	// A cut in the middle of a character at the end of the sample is fine.
	return !utf8.Valid(head[:len(head)-trailingPartialRune(head)])
}

// trailingPartialRune returns how many bytes at the end of b begin a character
// that is cut off.
func trailingPartialRune(b []byte) int {
	for i := 1; i <= utf8.UTFMax && i <= len(b); i++ {
		c := b[len(b)-i]
		if utf8.RuneStart(c) {
			if c >= 0xC0 && !utf8.FullRune(b[len(b)-i:]) {
				return i
			}
			return 0
		}
	}
	return 0
}

// layout walks text as it will be drawn, wrapped at width cells, calling cell
// for every character to show, and returns the number of rows used. Tabs become
// spaces, carriage returns are skipped and other control characters shown as a
// dot.
func layout(text []byte, width int, cell func(row, col int, r rune)) (rows int) {
	if width < 1 {
		return 0
	}
	row, col := 0, 0
	started := false
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRune(text[i:])
		i += size
		started = true
		switch {
		case r == '\n':
			row, col, started = row+1, 0, false
			continue
		case r == '\r':
			continue
		case r == '\t':
			for n := 4 - col%4; n > 0; n-- {
				if col >= width {
					row, col = row+1, 0
				}
				if cell != nil {
					cell(row, col, ' ')
				}
				col++
			}
			continue
		case r < ' ' || r == 0x7f || r == utf8.RuneError:
			r = '·'
		}
		w := runewidth.RuneWidth(r)
		if w == 0 {
			continue // a combining mark: it has no cell of its own
		}
		if col+w > width {
			row, col = row+1, 0
		}
		if cell != nil {
			cell(row, col, r)
		}
		col += w
	}
	if started || col > 0 {
		row++
	}
	return row
}

func (p *preview) Draw(screen tcell.Screen) {
	p.DrawForSubclass(screen, p)
	x, y, w, h := p.GetInnerRect()
	p.width, p.height = w, h
	if w < 1 || h < 1 {
		return
	}

	if p.text == nil {
		row := 0
		for _, l := range p.message {
			if row >= h {
				break
			}
			if l.text == "" {
				row++
				continue
			}
			top := row
			row += layout([]byte(l.text), w-2, func(r, c int, ch rune) {
				if top+r < h {
					screen.SetContent(x+1+c, y+top+r, ch, nil, l.style)
				}
			})
		}
		return
	}

	text := p.text
	note := ""
	switch {
	case looksBinary(text):
		drawText(screen, x+1, y, w-2, fmt.Sprintf("Binary data (%d bytes): not shown.", len(text)), bold)
		return
	case len(text) > maxPreview:
		text, note = text[:maxPreview], fmt.Sprintf("[cut off: showing the first %d of %d bytes]", maxPreview, len(p.text))
	}

	p.rows = layout(text, w, nil)
	if p.offset > p.rows-h {
		p.offset = p.rows - h
	}
	if p.offset < 0 {
		p.offset = 0
	}
	style := plain
	layout(text, w, func(row, col int, r rune) {
		if row >= p.offset && row < p.offset+h {
			screen.SetContent(x+col, y+row-p.offset, r, nil, style)
		}
	})
	if note != "" {
		drawText(screen, x, y+h-1, w, note, invert)
	}
}

func (p *preview) scroll(delta int) {
	p.offset += delta
	if max := p.rows - p.height; p.offset > max {
		p.offset = max
	}
	if p.offset < 0 {
		p.offset = 0
	}
}

func (p *preview) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return p.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		page := p.height - 1
		if page < 1 {
			page = 1
		}
		switch event.Key() {
		case tcell.KeyUp:
			p.scroll(-1)
		case tcell.KeyDown:
			p.scroll(1)
		case tcell.KeyPgUp:
			p.scroll(-page)
		case tcell.KeyPgDn:
			p.scroll(page)
		case tcell.KeyHome:
			p.offset = 0
		case tcell.KeyEnd:
			p.scroll(p.rows)
		case tcell.KeyRune:
			switch event.Rune() {
			case 'k':
				p.scroll(-1)
			case 'j':
				p.scroll(1)
			case ' ':
				p.scroll(page)
			case 'g':
				p.offset = 0
			case 'G':
				p.scroll(p.rows)
			}
		}
	})
}
