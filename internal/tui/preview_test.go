package tui

import (
	"strings"
	"testing"
)

func TestLayoutWrapsAndCountsRows(t *testing.T) {
	type cell struct {
		row, col int
		r        rune
	}
	run := func(text string, width int) (rows int, cells []cell) {
		rows = layout([]byte(text), width, func(row, col int, r rune) { cells = append(cells, cell{row, col, r}) })
		return
	}
	for _, tc := range []struct {
		name  string
		text  string
		width int
		rows  int
	}{
		{"empty", "", 10, 0},
		{"one line", "hello", 10, 1},
		{"trailing newline is not an extra row", "hello\n", 10, 1},
		{"blank line counts", "a\n\nb", 10, 3},
		{"wraps", "abcdefghij", 4, 3},
		{"exact fit", "abcd", 4, 1},
		{"newline only", "\n", 10, 1},
		{"carriage returns are skipped", "a\r\nb\r\n", 10, 2},
		{"wide characters take two cells", "漢字漢字", 4, 2},
		{"tab", "a\tb", 10, 1},
	} {
		if rows, _ := run(tc.text, tc.width); rows != tc.rows {
			t.Errorf("%s: %d rows, want %d", tc.name, rows, tc.rows)
		}
	}

	// Positions.
	_, cells := run("ab\ncd", 10)
	want := []cell{{0, 0, 'a'}, {0, 1, 'b'}, {1, 0, 'c'}, {1, 1, 'd'}}
	for i, w := range want {
		if cells[i] != w {
			t.Errorf("cell %d: %v, want %v", i, cells[i], w)
		}
	}
	_, cells = run("a\tb", 10)
	if cells[len(cells)-1] != (cell{0, 4, 'b'}) {
		t.Errorf("a tab should stop at column 4: %v", cells)
	}
	_, cells = run("a\x01b", 10)
	if cells[1].r != '·' {
		t.Errorf("a control character should show as a dot: %q", cells[1].r)
	}
	if rows := layout([]byte("x"), 0, nil); rows != 0 {
		t.Errorf("zero width: %d rows", rows)
	}
}

func TestLooksBinary(t *testing.T) {
	for text, want := range map[string]bool{
		"plain ascii":                 false,
		"héllo wörld 漢字":              false,
		"has a \x00 nul":              true,
		"\xff\xfe invalid utf8":       true,
		"":                            false,
		"cut in the middle: \xe6\xbc": false, // a character cut off at the end of the sample
	} {
		if got := looksBinary([]byte(text)); got != want {
			t.Errorf("%q: got %v, want %v", text, got, want)
		}
	}
	// Only the start is sampled, so a late NUL doesn't make a big text binary.
	if looksBinary([]byte(strings.Repeat("a", 20000) + "\x00")) {
		t.Error("a NUL far into a long file made it binary")
	}
}
