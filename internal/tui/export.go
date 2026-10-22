package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"filippo.io/age"

	"github.com/eddienko/secure-edit/internal/share"
	"github.com/eddienko/secure-edit/internal/vault"
)

// exportDir is the folder, inside the top folder, that exported copies go to.
const exportDir = "exported"

const exportHint = "One per line: an age key (age1...), an SSH public key (ssh-ed25519 ...),\n" +
	"or the path of a file of keys. Ctrl-S continues."

// exportCurrent makes a copy of the selected file for someone else: the text
// encrypted to their public keys in the age format, which they open with plain
// "age -d" or "sedit --import". It asks for the recipients, then whether to write
// the copy to the exported folder or to put it on the clipboard as text.
func (a *App) exportCurrent() {
	e, ok := a.current()
	if !ok || e.Kind != KindSedit {
		a.say("select a sedit file to export", true)
		return
	}
	var recips []age.Recipient
	a.fieldsDialog("export", "Export "+filepath.Base(e.Path)+" for someone else", exportHint,
		[]formField{{Label: "Recipients", Lines: 4}},
		func(vals []string) (int, error) {
			var err error
			recips, err = share.ParseList(vals[0])
			return 0, err
		},
		func(_ []string, ok bool) {
			if !ok {
				return
			}
			who := plural(len(recips), "recipient", "recipients")
			a.choiceDialog("Export "+filepath.Base(e.Path)+" to "+who+"?\n\nThey will be able to read the whole text.",
				[]string{"Write a file", "Copy as text", "Cancel"}, func(i int) {
					if i == 0 || i == 1 {
						a.export(e, recips, i == 1)
					}
				})
		})
}

// export unlocks the file and writes its text, encrypted to recips, into the
// exported folder or, as armored text, to the clipboard.
func (a *App) export(e Entry, recips []age.Recipient, asText bool) {
	a.unlockHidden(func(s *Session, finish func()) {
		data, err := share.Encrypt(s.Plain, recips, asText)
		finish() // the text is not needed any more
		if err != nil {
			a.say("couldn't export: "+err.Error(), true)
			return
		}
		if asText {
			c, err := a.clipboard()
			if err == nil {
				err = c.Set(data)
			}
			if err != nil {
				a.say("couldn't copy: "+err.Error(), true)
				return
			}
			// It is encrypted: no need to clear it again, and a timer could wipe
			// something the user copied since.
			a.say(fmt.Sprintf("copied %s, encrypted, as text (%d bytes); paste it where you like", filepath.Base(e.Path), len(data)), false)
			return
		}
		out, err := a.exportedPath(e.Path)
		if err == nil {
			err = vault.WriteAtomic(out, data)
		}
		if err != nil {
			a.say("couldn't export: "+err.Error(), true)
			return
		}
		a.reload(e.Path)
		rel, _ := filepath.Rel(a.root, out)
		a.say(fmt.Sprintf("exported to %s; they open it with: age -d -i KEY, or sedit --import", rel), false)
	})
}

// exportedPath is a new file name for the copy of src in the exported folder,
// which is made if needed. An earlier export is never overwritten.
func (a *App) exportedPath(src string) (string, error) {
	dir := filepath.Join(a.root, exportDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	base := filepath.Base(src)
	for n := 1; n < 1000; n++ {
		name := base + ".age"
		if n > 1 {
			name = fmt.Sprintf("%s.%d.age", base, n)
		}
		out := filepath.Join(dir, name)
		if _, err := os.Lstat(out); errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
	}
	return "", errors.New("too many exports of this file")
}
