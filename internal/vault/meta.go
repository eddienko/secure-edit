package vault

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits on the title and description, in bytes. The description is meant for a
// few lines, not a document.
const (
	MaxTitle       = 200
	MaxDescription = 2000
)

// Meta is a file's title and description. They sit in the file header in the
// clear, so that a program can show them before the file is unlocked. They are
// labels, not secrets: anyone who has the file can read them.
type Meta struct {
	Title       string
	Description string
}

// IsZero reports whether there is neither a title nor a description.
func (m Meta) IsZero() bool { return m.Title == "" && m.Description == "" }

// Clean trims the surrounding space and turns line endings into \n.
func (m Meta) Clean() Meta {
	nl := strings.NewReplacer("\r\n", "\n", "\r", "\n")
	return Meta{
		Title:       strings.TrimSpace(m.Title),
		Description: strings.TrimSpace(nl.Replace(m.Description)),
	}
}

// Validate checks the length limits and that the text is printable. The title
// is a single line; the description may have several. Control characters are
// refused because they would be drawn on someone's terminal.
func (m Meta) Validate() error {
	if len(m.Title) > MaxTitle {
		return fmt.Errorf("the title is too long (%d bytes; at most %d)", len(m.Title), MaxTitle)
	}
	if len(m.Description) > MaxDescription {
		return fmt.Errorf("the description is too long (%d bytes; at most %d)", len(m.Description), MaxDescription)
	}
	if err := printable(m.Title, false); err != nil {
		return fmt.Errorf("the title %w", err)
	}
	if err := printable(m.Description, true); err != nil {
		return fmt.Errorf("the description %w", err)
	}
	return nil
}

func printable(s string, newlines bool) error {
	if !utf8.ValidString(s) {
		return errors.New("is not valid UTF-8")
	}
	for _, r := range s {
		if r == '\n' && newlines {
			continue
		}
		if unicode.IsControl(r) {
			return errors.New("has a control character in it")
		}
	}
	return nil
}
