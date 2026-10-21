package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eddienko/secure-edit/internal/fileops"
	"github.com/eddienko/secure-edit/internal/vault"
)

// Folders can be made, given a title and description, renamed, moved (with the
// files in them) and, when empty, deleted. The real work is in fileops.

// folderHint is at the bottom of the dialogs that ask for a folder's title and
// description.
const folderHint = "Title and description are stored in the clear: keep secrets out of them.\n" +
	"In the description Enter starts a new line; Ctrl-S saves from any field."

// isTop reports whether e is the folder being browsed, which can't be renamed,
// moved or deleted from inside the interface.
func (a *App) isTop(e Entry) bool { return e.Path == a.root }

// summary says what is in a folder.
func summary(dir string) string {
	es, err := List(dir)
	if err != nil {
		return err.Error()
	}
	var sedit, other, dirs int
	for _, e := range es {
		switch e.Kind {
		case KindDir:
			dirs++
		case KindSedit:
			sedit++
		default:
			other++
		}
	}
	var parts []string
	if sedit > 0 {
		parts = append(parts, plural(sedit, "sedit file", "sedit files"))
	}
	if other > 0 {
		parts = append(parts, plural(other, "other file", "other files"))
	}
	if dirs > 0 {
		parts = append(parts, plural(dirs, "folder", "folders"))
	}
	if len(parts) == 0 {
		return "Empty folder."
	}
	return strings.Join(parts, ", ")
}

// showFolder fills the preview for a folder.
func (a *App) showFolder(e Entry) {
	keys := "Enter opens or closes it.\nn new file    N new folder    i title and description"
	if !a.isTop(e) {
		keys += "\nr rename    m move    d delete (when empty)"
	}
	a.prev.SetTitle(a.paneTitle(e.Meta.Title, e.Name+"/", ""))
	a.prev.showFolder(e.Meta, summary(e.Path), keys)
}

// background runs work off the interface's thread, with msg on the bar, while
// keys are ignored; done runs back on it. A folder with many files means one call
// to the Keychain for each, which should not freeze the screen.
func (a *App) background(msg string, work func(), done func()) {
	a.busy = true
	a.say(msg, false)
	go func() {
		work()
		a.app.QueueUpdateDraw(func() {
			a.busy = false
			a.renderStatus()
			done()
		})
	}()
}

// moveExpanded keeps the folders that were open open after a folder is renamed
// or moved: it rewrites their paths.
func (a *App) moveExpanded(from, to string) {
	for p := range a.expanded {
		if p == from || strings.HasPrefix(p, from+string(filepath.Separator)) {
			delete(a.expanded, p)
			a.expanded[to+strings.TrimPrefix(p, from)] = true
		}
	}
}

// moveDirText says what a folder move did: the start of the message, then what
// came along.
func moveDirText(msg string, res fileops.Result) string {
	var parts []string
	if res.Files > 0 {
		parts = append(parts, plural(res.Files, "sedit file", "sedit files"))
	}
	if res.Passwords > 0 {
		parts = append(parts, plural(res.Passwords, "stored password", "stored passwords"))
	}
	if len(parts) > 0 {
		msg += " (with " + strings.Join(parts, " and ") + ")"
	}
	return msg
}

// newFolder asks for a name, title and description in one box and makes the
// folder in the selected folder (or the selected file's).
func (a *App) newFolder() {
	dir := a.targetDir()
	a.fieldsDialog("newfolder", "New folder in "+filepath.Base(dir)+"  (Tab: next, Ctrl-S: create, Esc: cancel)", folderHint,
		[]formField{{Label: "Name:"}, {Label: "Title:"}, {Label: "Description:", Lines: descriptionLines}},
		func(v []string) (int, error) {
			name := strings.TrimSpace(v[0])
			if err := fileops.ValidName(name); err != nil {
				return 0, err
			}
			if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
				return 0, errors.New(name + " already exists")
			}
			return checkLabels(v[1], v[2])
		},
		func(v []string, ok bool) {
			if !ok {
				return
			}
			path, err := fileops.MkDir(dir, strings.TrimSpace(v[0]), labelsFrom(v[1], v[2]))
			if err != nil {
				a.say(err.Error(), true)
				return
			}
			a.expanded[dir] = true
			a.reload(path)
			a.say("made folder "+filepath.Base(path)+"/", false)
		})
}

// renameFolder asks for a new name for the folder e, which stays where it is.
// (To move it elsewhere, m.)
func (a *App) renameFolder(e Entry) {
	if a.isTop(e) {
		a.say("the folder you opened can't be renamed from here", true)
		return
	}
	a.inputDialog("Rename folder "+e.Name+"/", "New name:", e.Name, func(name string, ok bool) {
		name = strings.TrimSpace(name)
		if !ok || name == "" || name == e.Name {
			return
		}
		if err := fileops.ValidName(name); err != nil {
			a.say(err.Error(), true)
			return
		}
		a.moveFolder(e, filepath.Join(filepath.Dir(e.Path), name), "renamed to "+name+"/")
	})
}

// moveFolder moves the folder e to dest, in the background.
func (a *App) moveFolder(e Entry, dest, verb string) {
	a.lock() // nothing inside may be open while it moves
	var res fileops.Result
	var err error
	a.background("moving "+e.Name+"/ …", func() { res, err = fileops.MoveDir(e.Path, dest) }, func() {
		a.reportOps(res)
		if err != nil {
			a.say(err.Error(), true)
			a.reload("")
			return
		}
		a.moveExpanded(e.Path, dest)
		a.expanded[filepath.Dir(dest)] = true
		a.reload(dest)
		a.say(moveDirText(verb, res), false)
	})
}

// folderChoices are the folders a move can go to, with the top folder first.
func (a *App) folderChoices(e Entry) (paths []string, items []pickItem) {
	add := func(path, label, detail string) {
		paths = append(paths, path)
		items = append(items, pickItem{Label: label, Detail: detail})
	}
	skip := func(path string) bool {
		if path == filepath.Dir(e.Path) { // where it is already
			return true
		}
		return e.Kind == KindDir && (path == e.Path || strings.HasPrefix(path, e.Path+string(filepath.Separator)))
	}
	if !skip(a.root) {
		add(a.root, "/  (this whole folder)", vault.ReadFolderMeta(a.root).Title)
	}
	for _, f := range walk(a.root) {
		if f.Kind != KindDir || skip(f.Path) {
			continue
		}
		rel, err := filepath.Rel(a.root, f.Path)
		if err != nil {
			continue
		}
		add(f.Path, rel+"/", f.Meta.Title)
	}
	return paths, items
}

// moveCurrent asks which folder to move the selected file or folder to.
func (a *App) moveCurrent() {
	e, ok := a.current()
	switch {
	case !ok || (e.Kind != KindSedit && e.Kind != KindDir):
		a.say("select a sedit file or a folder to move", true)
		return
	case a.isTop(e):
		a.say("the folder you opened can't be moved from here", true)
		return
	}
	paths, items := a.folderChoices(e)
	if len(items) == 0 {
		a.say("there is no other folder to move it to; make one with N", true)
		return
	}
	name := e.Name
	if e.Kind == KindDir {
		name += "/"
	}
	a.pickDialog("pick", "Move "+name+" to", items, func(i int, ok bool) {
		if !ok {
			return
		}
		target := paths[i]
		if e.Kind == KindDir {
			a.moveFolder(e, filepath.Join(target, e.Name), "moved "+e.Name+"/")
			return
		}
		a.lock()
		dest, res, err := fileops.Move(e.Path, target)
		a.reportOps(res)
		if err != nil {
			a.say(err.Error(), true)
			return
		}
		a.expanded[target] = true
		a.reload(dest)
		a.say("moved "+e.Name+" to "+items[i].Label+alsoText(res), false)
	})
}

// editFolderInfo changes the title and description of a folder. There is
// nothing to unlock: they are not encrypted.
func (a *App) editFolderInfo(e Entry) {
	old := vault.ReadFolderMeta(e.Path)
	a.fieldsDialog("info", "Title and description of "+e.Name+"/  (Ctrl-S: save, Esc: cancel)", folderHint,
		[]formField{{Label: "Title:", Initial: old.Title}, {Label: "Description:", Initial: old.Description, Lines: descriptionLines}},
		func(v []string) (int, error) {
			i, err := checkLabels(v[0], v[1])
			return i - 1, err
		},
		func(v []string, ok bool) {
			if !ok {
				return
			}
			m := labelsFrom(v[0], v[1])
			if m == old {
				a.say("the title and description of "+e.Name+"/ are unchanged", false)
				return
			}
			if err := vault.WriteFolderMeta(e.Path, m); err != nil {
				a.say(err.Error(), true)
				return
			}
			a.reload(e.Path)
			a.say("updated the title and description of "+e.Name+"/", false)
		})
}

// removeFolder deletes the folder e if it is empty; otherwise it says what is in
// it. Nothing else is ever deleted with a folder.
func (a *App) removeFolder(e Entry) {
	if a.isTop(e) {
		a.say("the folder you opened can't be deleted from here", true)
		return
	}
	if err := fileops.CheckEmptyDir(e.Path); err != nil {
		a.say(err.Error()+"; delete or move it first", true)
		return
	}
	a.choiceDialog(fmt.Sprintf("Delete the empty folder %s/?\n\nThis can't be undone.", e.Name),
		[]string{"Cancel", "Delete"}, func(i int) {
			if i != 1 {
				return
			}
			if err := fileops.RemoveEmptyDir(e.Path); err != nil {
				a.say(err.Error(), true)
				a.reload("")
				return
			}
			delete(a.expanded, e.Path)
			a.reload(filepath.Dir(e.Path))
			a.say("deleted folder "+e.Name+"/", false)
		})
}
