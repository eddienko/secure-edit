package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	dim    = tcell.StyleDefault.Dim(true)
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
	hints func(width int) string // what to show when there is no message
}

func newStatusBar() *statusBar { return &statusBar{Box: tview.NewBox(), style: invert} }

func (s *statusBar) set(text string, style tcell.Style) { s.text, s.style = text, style }

func (s *statusBar) Draw(screen tcell.Screen) {
	s.DrawForSubclass(screen, s)
	x, y, w, _ := s.GetInnerRect()
	for i := 0; i < w; i++ {
		screen.SetContent(x+i, y, ' ', nil, s.style)
	}
	text := s.text
	if text == "" && s.hints != nil {
		text = s.hints(w - 2)
	}
	drawText(screen, x+1, y, w-2, text, s.style)
}

// trimTo cuts s to at most width cells, ending in … if anything was cut.
func trimTo(s string, width int) string {
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if width < 1 {
		return ""
	}
	return runewidth.Truncate(s, width, "…")
}

// words says a duration the way a person would: "2 minutes", "30 seconds".
func words(d time.Duration) string {
	plural := func(n int, unit string) string {
		if n == 1 {
			return "1 " + unit
		}
		return fmt.Sprintf("%d %ss", n, unit)
	}
	secs := int(d.Round(time.Second) / time.Second)
	switch {
	case secs%60 == 0 && secs >= 60:
		return plural(secs/60, "minute")
	case secs < 60:
		return plural(secs, "second")
	}
	return plural(secs/60, "minute") + " " + plural(secs%60, "second")
}

// safeText makes text from a file's header fit for the screen. The title and
// description are readable without the password, so nothing vouches for them:
// control characters, which could move the cursor or retitle the window on some
// terminals, are shown as a dot. Line breaks are kept if keepNewlines is set and
// become spaces otherwise.
func safeText(s string, keepNewlines bool) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' && keepNewlines:
			return r
		case r == '\n' || r == '\t' || r == '\r':
			return ' '
		case unicode.IsControl(r) || r == utf8.RuneError:
			return '·'
		}
		return r
	}, s)
}
