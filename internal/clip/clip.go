// Package clip puts text on the system clipboard and takes it off again. It
// uses the platform's own tools (pbcopy and pbpaste on macOS; wl-clipboard,
// xclip or xsel elsewhere), so the text goes straight to them on stdin and
// never appears on a command line.
//
// A secret on the clipboard is a risk: clipboard managers may record it, and
// on macOS Universal Clipboard can send it to your other devices. Holder copies
// text for a limited time and then clears it, but only if the clipboard still
// holds that text, so it never wipes something the user copied since.
package clip

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Clipboard is the system clipboard.
type Clipboard interface {
	Set(data []byte) error
	Get() ([]byte, error)
	Clear() error
}

// ErrNoTool is returned by System when there is no clipboard tool to use.
var ErrNoTool = errors.New("no clipboard tool found (on macOS pbcopy, on Linux install wl-clipboard, xclip or xsel)")

// tool describes a command-line clipboard tool.
type tool struct {
	copyCmd  []string
	pasteCmd []string
	clearCmd []string // if empty, clearing means copying nothing
	needs    string   // an environment variable that must be set, such as DISPLAY
}

var tools = []tool{
	{copyCmd: []string{"pbcopy"}, pasteCmd: []string{"pbpaste"}},
	{copyCmd: []string{"wl-copy"}, pasteCmd: []string{"wl-paste", "--no-newline"}, clearCmd: []string{"wl-copy", "--clear"}, needs: "WAYLAND_DISPLAY"},
	{copyCmd: []string{"xclip", "-selection", "clipboard", "-in"}, pasteCmd: []string{"xclip", "-selection", "clipboard", "-out"}, needs: "DISPLAY"},
	{copyCmd: []string{"xsel", "--clipboard", "--input"}, pasteCmd: []string{"xsel", "--clipboard", "--output"}, needs: "DISPLAY"},
}

type system struct{ tool }

// System returns the clipboard through the first usable tool, or ErrNoTool.
func System() (Clipboard, error) {
	for _, t := range tools {
		if t.needs != "" && os.Getenv(t.needs) == "" {
			continue
		}
		_, e1 := exec.LookPath(t.copyCmd[0])
		_, e2 := exec.LookPath(t.pasteCmd[0])
		if e1 == nil && e2 == nil {
			return system{t}, nil
		}
	}
	return nil, ErrNoTool
}

func (s system) Set(data []byte) error {
	cmd := exec.Command(s.copyCmd[0], s.copyCmd[1:]...)
	cmd.Stdin = bytes.NewReader(data) // on stdin, never on the command line
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w %s", s.copyCmd[0], err, bytes.TrimSpace(out))
	}
	return nil
}

func (s system) Get() ([]byte, error) {
	out, err := exec.Command(s.pasteCmd[0], s.pasteCmd[1:]...).Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.pasteCmd[0], err)
	}
	return out, nil
}

func (s system) Clear() error {
	if len(s.clearCmd) > 0 {
		if out, err := exec.Command(s.clearCmd[0], s.clearCmd[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w %s", s.clearCmd[0], err, bytes.TrimSpace(out))
		}
		return nil
	}
	return s.Set(nil)
}

// Result says what happened when the clipboard was due to be cleared.
type Result int

const (
	Cleared     Result = iota // the clipboard held our text, and is now empty
	LeftAlone                 // it held something else by then, which was not touched
	ClearFailed               // it could not be cleared
)

// Holder copies text to the clipboard for a limited time.
type Holder struct {
	c Clipboard

	mu      sync.Mutex
	timer   *time.Timer
	sum     [sha256.Size]byte // a hash of what we copied, so the text itself isn't kept
	holding bool
	gen     int // which copy a timer belongs to; a newer copy supersedes an older timer
}

// NewHolder returns a Holder for c.
func NewHolder(c Clipboard) *Holder { return &Holder{c: c} }

// Copy puts data on the clipboard and clears it after ttl, unless it has been
// replaced by then. done, which may be nil, is called from another goroutine
// when the timer fires. A newer Copy replaces an older one and its timer.
func (h *Holder) Copy(data []byte, ttl time.Duration, done func(Result, error)) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.c.Set(data); err != nil {
		return err
	}
	if h.timer != nil {
		h.timer.Stop()
	}
	h.sum, h.holding = sha256.Sum256(data), true
	h.gen++
	gen := h.gen
	h.timer = time.AfterFunc(ttl, func() {
		h.mu.Lock()
		if h.gen != gen || !h.holding { // superseded, or already cleared
			h.mu.Unlock()
			return
		}
		r, err := h.clearLocked()
		h.mu.Unlock()
		if done != nil {
			done(r, err)
		}
	})
	return nil
}

// ClearNow clears the clipboard immediately if it still holds what was copied.
// Call it when the program exits: the timer dies with the process. It reports
// whether there was anything of ours to clear.
func (h *Holder) ClearNow() (held bool, r Result, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.holding {
		return false, Cleared, nil
	}
	if h.timer != nil {
		h.timer.Stop()
	}
	r, err = h.clearLocked()
	return true, r, err
}

func (h *Holder) clearLocked() (Result, error) {
	h.holding = false
	// If the clipboard can't be read there is no way to tell whether it is still
	// ours; clearing is the safer mistake for a secret.
	if cur, err := h.c.Get(); err == nil && sha256.Sum256(cur) != h.sum {
		return LeftAlone, nil
	}
	if err := h.c.Clear(); err != nil {
		return ClearFailed, err
	}
	return Cleared, nil
}
