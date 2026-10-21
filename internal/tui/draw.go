package tui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"
)

// The interface uses no colours of its own. Every cell is drawn in the
// terminal's default foreground and background, and emphasis comes from
// attributes (bold, reverse video, underline) and from symbols. That keeps it
// readable in any terminal theme, light or dark, and in see-through profiles:
// a colour that is clear in one theme (grey, say, or dark blue) can be nearly
// invisible in another, but reverse video always swaps the theme's own text and
// background colours.
var (
	plain  = tcell.StyleDefault
	bold   = tcell.StyleDefault.Bold(true)
	invert = tcell.StyleDefault.Reverse(true)
	alert  = tcell.StyleDefault.Reverse(true).Bold(true)
)

// useTerminalColors makes every tview widget created from now on use the
// terminal's default colours. tview's own theme paints a black background with
// white text and blue buttons, regardless of the terminal.
func useTerminalColors() {
	d := tcell.ColorDefault
	tview.Styles = tview.Theme{
		PrimitiveBackgroundColor:    d,
		ContrastBackgroundColor:     d,
		MoreContrastBackgroundColor: d,
		BorderColor:                 d,
		TitleColor:                  d,
		GraphicsColor:               d,
		PrimaryTextColor:            d,
		SecondaryTextColor:          d,
		TertiaryTextColor:           d,
		InverseTextColor:            d,
		ContrastSecondaryTextColor:  d,
	}
}

// drawText draws one line of text, cut off at maxWidth cells.
func drawText(screen tcell.Screen, x, y, maxWidth int, text string, style tcell.Style) {
	col := 0
	for _, r := range text {
		w := runewidth.RuneWidth(r)
		if w == 0 {
			continue
		}
		if col+w > maxWidth {
			return
		}
		screen.SetContent(x+col, y, r, nil, style)
		col += w
	}
}

// statusBar is the one-row bar at the bottom, drawn in reverse video so that it
// stands out in any theme.
type statusBar struct {
	*tview.Box
	text  string
	style tcell.Style
}

func newStatusBar() *statusBar { return &statusBar{Box: tview.NewBox(), style: invert} }

func (s *statusBar) set(text string, style tcell.Style) { s.text, s.style = text, style }

func (s *statusBar) Draw(screen tcell.Screen) {
	s.DrawForSubclass(screen, s)
	x, y, w, _ := s.GetInnerRect()
	for i := 0; i < w; i++ {
		screen.SetContent(x+i, y, ' ', nil, s.style)
	}
	drawText(screen, x+1, y, w-2, s.text, s.style)
}
