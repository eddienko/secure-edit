package clip

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fake is an in-memory clipboard.
type fake struct {
	mu                       sync.Mutex
	data                     []byte
	setErr, getErr, clearErr error
	clears                   int
}

func (f *fake) Set(d []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	f.data = append([]byte(nil), d...)
	return nil
}
func (f *fake) Get() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	return append([]byte(nil), f.data...), nil
}
func (f *fake) Clear() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.clearErr != nil {
		return f.clearErr
	}
	f.data, f.clears = nil, f.clears+1
	return nil
}
func (f *fake) text() string { f.mu.Lock(); defer f.mu.Unlock(); return string(f.data) }

type outcome struct {
	r   Result
	err error
}

func waitOutcome(t *testing.T, ch chan outcome) outcome {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(3 * time.Second):
		t.Fatal("the clipboard was never cleared")
		return outcome{}
	}
}

func collect() (func(Result, error), chan outcome) {
	ch := make(chan outcome, 4)
	return func(r Result, err error) { ch <- outcome{r, err} }, ch
}

func TestCopyThenClearAfterTheTime(t *testing.T) {
	f := &fake{}
	h := NewHolder(f)
	done, ch := collect()
	if err := h.Copy([]byte("secret text"), 80*time.Millisecond, done); err != nil {
		t.Fatal(err)
	}
	if f.text() != "secret text" {
		t.Fatalf("clipboard holds %q", f.text())
	}
	if o := waitOutcome(t, ch); o.r != Cleared || o.err != nil {
		t.Fatalf("got %+v", o)
	}
	if f.text() != "" {
		t.Errorf("clipboard still holds %q", f.text())
	}
}

// Something the user copied since must not be wiped.
func TestClipboardChangedMeanwhileIsLeftAlone(t *testing.T) {
	f := &fake{}
	h := NewHolder(f)
	done, ch := collect()
	h.Copy([]byte("secret"), 80*time.Millisecond, done)
	f.Set([]byte("the user's own, newer copy"))
	if o := waitOutcome(t, ch); o.r != LeftAlone {
		t.Fatalf("got %+v", o)
	}
	if f.text() != "the user's own, newer copy" || f.clears != 0 {
		t.Errorf("the clipboard was touched: %q, %d clears", f.text(), f.clears)
	}
}

func TestACopyOfTheSameTextCountsAsOurs(t *testing.T) {
	f := &fake{}
	h := NewHolder(f)
	done, ch := collect()
	h.Copy([]byte("same"), 60*time.Millisecond, done)
	f.Set([]byte("same")) // the user copied the very same text
	if o := waitOutcome(t, ch); o.r != Cleared {
		t.Fatalf("got %+v", o)
	}
}

func TestANewerCopyReplacesTheOlderTimer(t *testing.T) {
	f := &fake{}
	h := NewHolder(f)
	done, ch := collect()
	h.Copy([]byte("first"), 150*time.Millisecond, done)
	time.Sleep(100 * time.Millisecond)
	h.Copy([]byte("second"), 300*time.Millisecond, done)
	time.Sleep(120 * time.Millisecond) // past the first timer's deadline
	if f.text() != "second" {
		t.Fatalf("the first timer cleared the newer copy: %q", f.text())
	}
	if o := waitOutcome(t, ch); o.r != Cleared {
		t.Fatalf("got %+v", o)
	}
	select {
	case o := <-ch:
		t.Fatalf("the old timer reported too: %+v", o)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestClearNowOnExit(t *testing.T) {
	f := &fake{}
	h := NewHolder(f)
	done, ch := collect()
	h.Copy([]byte("secret"), time.Hour, done)
	held, r, err := h.ClearNow()
	if !held || r != Cleared || err != nil || f.text() != "" {
		t.Fatalf("held=%v r=%v err=%v clipboard=%q", held, r, err, f.text())
	}
	// Nothing more to do, and the timer must not fire later.
	if held, _, _ := h.ClearNow(); held {
		t.Error("claimed to hold something after clearing")
	}
	select {
	case o := <-ch:
		t.Fatalf("the timer fired after ClearNow: %+v", o)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestClearNowLeavesALaterCopyAlone(t *testing.T) {
	f := &fake{}
	h := NewHolder(f)
	h.Copy([]byte("secret"), time.Hour, nil)
	f.Set([]byte("something else"))
	held, r, _ := h.ClearNow()
	if !held || r != LeftAlone || f.text() != "something else" {
		t.Fatalf("held=%v r=%v clipboard=%q", held, r, f.text())
	}
}

func TestClearNowWithNothingCopied(t *testing.T) {
	f := &fake{data: []byte("the user's clipboard")}
	if held, _, _ := NewHolder(f).ClearNow(); held || f.text() != "the user's clipboard" {
		t.Fatalf("held=%v clipboard=%q", held, f.text())
	}
}

func TestUnreadableClipboardIsClearedAnyway(t *testing.T) {
	f := &fake{getErr: errors.New("can't paste")}
	h := NewHolder(f)
	done, ch := collect()
	h.Copy([]byte("secret"), 50*time.Millisecond, done)
	if o := waitOutcome(t, ch); o.r != Cleared || f.clears != 1 {
		t.Fatalf("got %+v, %d clears", o, f.clears)
	}
}

func TestFailures(t *testing.T) {
	f := &fake{setErr: errors.New("no pasteboard")}
	if err := NewHolder(f).Copy([]byte("x"), time.Hour, nil); err == nil {
		t.Error("a failed copy returned no error")
	}

	f = &fake{clearErr: errors.New("busy")}
	h := NewHolder(f)
	done, ch := collect()
	h.Copy([]byte("x"), 40*time.Millisecond, done)
	if o := waitOutcome(t, ch); o.r != ClearFailed || o.err == nil {
		t.Fatalf("got %+v", o)
	}
}

func TestTheTextIsNotKeptByTheHolder(t *testing.T) {
	// The holder remembers a hash, not the text.
	h := NewHolder(&fake{})
	h.Copy([]byte("a very secret sentence"), time.Hour, nil)
	defer h.ClearNow()
	if len(h.sum) != 32 {
		t.Fatal("expected a SHA-256 hash")
	}
}

// ---- the real backend, against stand-in tools ---------------------------

// standIn puts fake clipboard tools first on PATH, backed by a file, so that
// the tests never touch the real clipboard.
func standIn(t *testing.T, tools ...string) (store string) {
	t.Helper()
	dir := t.TempDir()
	store = filepath.Join(dir, "clipboard")
	os.WriteFile(store, nil, 0o600)
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range tools {
		switch name {
		case "pbcopy":
			write("pbcopy", "/bin/cat > "+store+"\n")
		case "pbpaste":
			write("pbpaste", "/bin/cat "+store+"\n")
		case "wl-copy":
			write("wl-copy", `if [ "$1" = "--clear" ]; then : > `+store+`; else /bin/cat > `+store+`; fi`+"\n")
		case "wl-paste":
			write("wl-paste", "/bin/cat "+store+"\n")
		case "xclip":
			write("xclip", `case "$*" in *-in*) /bin/cat > `+store+`;; *) /bin/cat `+store+`;; esac`+"\n")
		}
	}
	t.Setenv("PATH", dir)
	return store
}

func TestSystemUsesPbcopyAndPbpaste(t *testing.T) {
	store := standIn(t, "pbcopy", "pbpaste")
	c, err := System()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Set([]byte("line one\nline two\n  ünïcode  ")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(store); string(b) != "line one\nline two\n  ünïcode  " {
		t.Errorf("the tool received %q", b)
	}
	if got, err := c.Get(); err != nil || string(got) != "line one\nline two\n  ünïcode  " {
		t.Errorf("Get: %q %v", got, err)
	}
	if err := c.Clear(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(store); len(b) != 0 {
		t.Errorf("not cleared: %q", b)
	}
}

// The text goes to the tool on stdin, never on its command line (where any
// user could see it in the process list).
func TestTheTextIsNeverOnTheCommandLine(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	os.WriteFile(filepath.Join(dir, "pbcopy"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+args+"\n/bin/cat > /dev/null\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "pbpaste"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", dir)
	c, _ := System()
	if err := c.Set([]byte("TOPSECRET")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(args); strings.Contains(string(b), "TOPSECRET") {
		t.Errorf("the secret was passed as an argument: %q", b)
	}
}

func TestSystemFindsTheOtherTools(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", "")
	store := standIn(t, "wl-copy", "wl-paste")
	c, err := System()
	if err != nil {
		t.Fatal(err)
	}
	c.Set([]byte("via wayland"))
	if got, _ := c.Get(); string(got) != "via wayland" {
		t.Errorf("Get: %q", got)
	}
	if err := c.Clear(); err != nil { // wl-copy --clear
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(store); len(b) != 0 {
		t.Errorf("not cleared: %q", b)
	}

	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", ":0")
	standIn(t, "xclip")
	c, err = System()
	if err != nil {
		t.Fatal(err)
	}
	c.Set([]byte("via x11"))
	if got, _ := c.Get(); string(got) != "via x11" {
		t.Errorf("xclip Get: %q", got)
	}
}

func TestSystemWithoutAnyToolSaysSo(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := System(); !errors.Is(err, ErrNoTool) {
		t.Fatalf("got %v", err)
	}
	// A display tool is useless without a display.
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	standIn(t, "xclip")
	if _, err := System(); !errors.Is(err, ErrNoTool) {
		t.Fatalf("xclip without DISPLAY: got %v", err)
	}
}

func TestAFailingToolIsReported(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pbcopy"), []byte("#!/bin/sh\necho 'pasteboard unavailable' >&2\nexit 1\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "pbpaste"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	t.Setenv("PATH", dir)
	c, _ := System()
	if err := c.Set([]byte("x")); err == nil || !strings.Contains(err.Error(), "pasteboard unavailable") {
		t.Errorf("Set: %v", err)
	}
	if _, err := c.Get(); err == nil {
		t.Error("Get: expected an error")
	}
}
