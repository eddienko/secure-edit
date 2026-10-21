package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eddienko/secure-edit/internal/keystore"
	"github.com/eddienko/secure-edit/internal/vault"
)

// The locked preview shows a few facts about the file, none of which needs its
// password: where it is, how big its text is (the cipher doesn't change the
// length), when it last changed, whether it has a backup, how it will unlock, and
// what format it is in. They come from the file's header and its entry in the
// folder. Only the "how it will unlock" line asks anything outside the file: the
// Keychain, whether an entry exists, never what it holds.

// fact is one line of the block: a label and its value.
type fact struct{ Label, Value string }

// defaultPasswordCheckDelay is how long the cursor must rest on a file before the
// Keychain is asked about it, so that scrolling through a list doesn't ask about
// every file on the way.
const defaultPasswordCheckDelay = 150 * time.Millisecond

// How the file will be unlocked, as far as the Keychain says.
type pwState int

const (
	pwUnknown  pwState = iota // not asked yet
	pwChecking                // being asked
	pwStored                  // the file has a stored password
	pwDefault                 // only the default password is stored
	pwAsk                     // neither: it will ask
)

// humanSize writes a number of bytes the way a person would.
func humanSize(n int64) string {
	switch {
	case n == 1:
		return "1 byte"
	case n < 1024:
		return fmt.Sprintf("%d bytes", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
}

// ago says how long before now t was: "3 days ago".
func ago(now, t time.Time) string {
	unit := func(n int, name string) string {
		if n == 1 {
			return "1 " + name + " ago"
		}
		return fmt.Sprintf("%d %ss ago", n, name)
	}
	d := now.Sub(t)
	switch {
	case d < 45*time.Second: // includes a modification time slightly in the future
		return "just now"
	case d < 45*time.Minute:
		return unit(int((d+30*time.Second)/time.Minute), "minute")
	case d < 36*time.Hour:
		return unit(int((d+30*time.Minute)/time.Hour), "hour")
	case d < 60*24*time.Hour:
		return unit(int((d+12*time.Hour)/(24*time.Hour)), "day")
	case d < 730*24*time.Hour:
		return unit(int((d+15*24*time.Hour)/(30*24*time.Hour)), "month")
	}
	return unit(int((d+182*24*time.Hour)/(365*24*time.Hour)), "year")
}

// stamp is a time as "18 Oct 2026, 15:48" in the local time zone.
func stamp(t time.Time) string { return t.Local().Format("2 Jan 2006, 15:04") }

// modified is how a file's change time is shown: "3 days ago (18 Oct 2026, 15:48)".
func modified(now, t time.Time) string { return ago(now, t) + " (" + stamp(t) + ")" }

// readHead reads the start of a file, enough for any header.
func readHead(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	head := make([]byte, maxHeader)
	n, _ := io.ReadFull(f, head)
	return head[:n]
}

// ageRecipients says, from the readable part of an age file's header, who can
// open it: a passphrase, or keys. An armored file has to be decoded first, which
// is not done here.
func ageRecipients(head []byte) string {
	if !bytes.HasPrefix(head, []byte("age-encryption.org/")) {
		return "ASCII-armored: the header is not read here"
	}
	var passphrase bool
	keys := 0
	for _, l := range strings.Split(string(head), "\n") {
		if l == "---" || strings.HasPrefix(l, "--- ") {
			break // the header ends here
		}
		if rest, ok := strings.CutPrefix(l, "-> "); ok {
			if strings.Fields(rest)[0] == "scrypt" {
				passphrase = true
			} else {
				keys++
			}
		}
	}
	switch {
	case passphrase:
		return "a passphrase"
	case keys == 1:
		return "1 key"
	case keys > 1:
		return fmt.Sprintf("%d keys", keys)
	}
	return "not recognised"
}

// where says which folder a file is in, relative to the one that was opened.
func (a *App) where(path string) string {
	rel, err := filepath.Rel(a.root, filepath.Dir(path))
	if err != nil || rel == "." {
		return "this folder"
	}
	return filepath.ToSlash(rel) + "/"
}

func (a *App) clock() time.Time {
	if a.opts.Now != nil {
		return a.opts.Now()
	}
	return time.Now()
}

// factsFor are the lines to show under a locked entry. A folder has none: its
// preview says what is in it.
func (a *App) factsFor(e Entry) []fact {
	now := a.clock()
	switch e.Kind {
	case KindSedit:
		return a.seditFacts(e, now)
	case KindAge:
		return []fact{
			{"Folder", a.where(e.Path)},
			{"Size", humanSize(e.Size)},
			{"Modified", modified(now, e.ModTime)},
			{"Opens with", ageRecipients(readHead(e.Path))},
		}
	case KindOther:
		return []fact{
			{"Folder", a.where(e.Path)},
			{"Size", humanSize(e.Size)},
			{"Modified", modified(now, e.ModTime)},
		}
	}
	return nil
}

func (a *App) seditFacts(e Entry, now time.Time) []fact {
	text, format := humanSize(e.Size), "not a valid sedit header"
	if info, err := vault.Describe(readHead(e.Path), e.Size); err == nil {
		text = humanSize(info.TextBytes)
		format = fmt.Sprintf("sedit v%d · Argon2id, %s, %s", info.Version,
			plural(int(info.Passes), "pass", "passes"), humanSize(int64(info.MemoryKiB)<<10))
		if info.Weaker() {
			format += " · weaker than the default"
		}
	}
	backup := "none"
	if fi, err := os.Stat(vault.BackupPath(e.Path)); err == nil {
		backup = "yes, from " + strings.TrimSuffix(ago(now, fi.ModTime()), " ago")
		if ago(now, fi.ModTime()) == "just now" {
			backup = "yes, from just now"
		}
	}
	return []fact{
		{"Folder", a.where(e.Path)},
		{"Text", text},
		{"Modified", modified(now, e.ModTime)},
		{"Backup", backup},
		{"Password", a.passwordFact(e)},
		{"Format", format},
	}
}

// passwordFact says how the file will be unlocked. The answer comes from the
// Keychain in the background, after the cursor has rested on the file for a
// moment, and is remembered until the folder is reloaded.
func (a *App) passwordFact(e Entry) string {
	if keystore.Backend == nil {
		return "will ask" // nothing is stored anywhere on this system
	}
	switch a.pwState[e.Path] {
	case pwStored:
		return "stored for this file"
	case pwDefault:
		return "the default password, if it fits; otherwise it will ask"
	case pwAsk:
		return "will ask"
	case pwUnknown:
		a.scheduleCheck(e.Path)
	}
	return "checking the Keychain …"
}

// scheduleCheck asks the Keychain about path once the cursor has rested on it.
func (a *App) scheduleCheck(path string) {
	if a.stopped.Load() {
		return // closing: locking redraws the preview, which must not start a new question
	}
	a.cancelCheck()
	if a.pendingCheck != "" && a.pendingCheck != path && a.pwState[a.pendingCheck] == pwChecking {
		delete(a.pwState, a.pendingCheck) // the cursor left it before it was asked
	}
	a.pendingCheck = path
	a.pwState[path] = pwChecking
	delay := a.opts.PasswordCheckDelay
	a.checks.Add(1) // done when the question has been asked, or the timer is stopped first
	a.checkTimer = time.AfterFunc(delay, func() {
		state := pwAsk
		if account, err := keystore.Account(path); err == nil {
			if ok, _ := keystore.Has(account); ok {
				state = pwStored
			} else if ok, _ := keystore.HasDefault(); ok {
				state = pwDefault
			}
		}
		// The Keychain has been asked: that is all endChecks waits for. Handing
		// the answer to the interface can block once it has stopped (the update
		// is never run), so it is not part of the wait.
		a.checks.Done()
		if a.stopped.Load() {
			return
		}
		a.app.QueueUpdateDraw(func() {
			if a.pwState[path] != pwChecking {
				return // the folder was reloaded in the meantime
			}
			a.pwState[path] = state
			if e, ok := a.current(); ok && e.Path == path && (a.session == nil || a.session.Locked() || a.session.Path != path) {
				a.refreshPreview()
			}
		})
	})
}

// cancelCheck drops the Keychain question that is waiting for the cursor to rest,
// if it has not started.
func (a *App) cancelCheck() {
	if a.checkTimer != nil && a.checkTimer.Stop() {
		a.checks.Done() // it will never run
	}
	a.checkTimer = nil
}

// endChecks stops asking the Keychain and waits for a question in progress, so
// that nothing is left running when the interface closes.
func (a *App) endChecks() {
	a.cancelCheck()
	a.checks.Wait()
}
