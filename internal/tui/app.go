package tui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/eddienko/secure-edit/internal/fileops"
	"github.com/eddienko/secure-edit/internal/keystore"
	"github.com/eddienko/secure-edit/internal/vault"
)

// DefaultIdle is how long an unlocked file stays unlocked without any key press.
const DefaultIdle = 2 * time.Minute

// Options configures an App. The zero value is fine for real use.
type Options struct {
	Idle      time.Duration // auto-lock after this long without input; DefaultIdle if zero
	TickEvery time.Duration // how often the idle timer is checked; one second if zero
	Screen    tcell.Screen  // for tests: a simulated screen
}

// App is the interface: a tree of files on the left, a locked preview on the
// right, and a status line.
type App struct {
	app    *tview.Application
	pages  *tview.Pages
	tree   *tview.TreeView
	prev   *preview
	status *tview.TextView

	root string
	opts Options

	session  *Session // the unlocked file, if any; at most one
	expanded map[string]bool
	parents  map[*tview.TreeNode]*tview.TreeNode
	last     time.Time // last key press
	msg      string
	msgErr   bool
}

// New creates the interface for the directory root.
func New(root string, opts Options) (*App, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	if opts.Idle <= 0 {
		opts.Idle = DefaultIdle
	}
	if opts.TickEvery <= 0 {
		opts.TickEvery = time.Second
	}
	a := &App{
		app:      tview.NewApplication(),
		pages:    tview.NewPages(),
		prev:     newPreview(),
		status:   tview.NewTextView().SetDynamicColors(true),
		root:     root,
		opts:     opts,
		expanded: map[string]bool{root: true},
		last:     time.Now(),
	}
	if opts.Screen != nil {
		a.app.SetScreen(opts.Screen)
	}

	a.tree = tview.NewTreeView().SetGraphics(true)
	a.tree.SetBorder(true).SetTitle(" " + treeTitle(root) + " ")
	a.tree.SetChangedFunc(a.onChanged)
	a.tree.SetSelectedFunc(a.onEnter)

	body := tview.NewFlex().
		AddItem(a.tree, 0, 1, true).
		AddItem(a.prev, 0, 2, false)
	main := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(body, 0, 1, true).
		AddItem(a.status, 1, 0, false)
	a.pages.AddPage("main", main, true, true)

	a.reload("")
	a.app.SetRoot(a.pages, true).SetFocus(a.tree)
	a.app.SetInputCapture(a.capture)
	a.renderStatus()
	return a, nil
}

// Run starts the interface and returns when it is closed.
func (a *App) Run() error {
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(a.opts.TickEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				a.app.QueueUpdateDraw(a.tick)
			}
		}
	}()
	err := a.app.Run()
	a.lock() // never leave secrets behind
	return err
}

// treeTitle is the title of the file list: the folder's name, with enough of its
// location to tell it apart, but short, since the pane is narrow.
func treeTitle(root string) string {
	if home, err := os.UserHomeDir(); err == nil && root != home {
		if rel, err := filepath.Rel(home, root); err == nil && !strings.HasPrefix(rel, "..") {
			root = filepath.Join("~", rel)
		}
	}
	if len([]rune(root)) > 28 {
		return "…/" + filepath.Base(root)
	}
	return root
}

// ---- the file tree ------------------------------------------------------

func (a *App) label(e Entry) (string, tcell.Color) {
	switch e.Kind {
	case KindDir:
		return e.Name + "/", tcell.ColorLightSkyBlue
	case KindSedit:
		return "▪ " + e.Name, tcell.ColorGreen
	case KindAge:
		return "○ " + e.Name, tcell.ColorYellow
	}
	return "· " + e.Name, tcell.ColorGray
}

func (a *App) newNode(e Entry) *tview.TreeNode {
	text, color := a.label(e)
	n := tview.NewTreeNode(text).SetReference(&e).SetColor(color).SetSelectable(true)
	if e.Kind == KindDir {
		n.SetExpanded(false)
	}
	return n
}

// fill (re)loads the children of a directory node.
func (a *App) fill(n *tview.TreeNode) {
	e := n.GetReference().(*Entry)
	n.ClearChildren()
	entries, err := List(e.Path)
	if err != nil {
		a.say(err.Error(), true)
		return
	}
	for _, c := range entries {
		child := a.newNode(c)
		a.parents[child] = n
		n.AddChild(child)
		if c.Kind == KindDir && a.expanded[c.Path] {
			a.fill(child)
			child.SetExpanded(true)
		}
	}
}

// reload rebuilds the tree from disk, keeping the folders that were open, and
// selects selectPath if it is there (otherwise the previous selection, if any).
func (a *App) reload(selectPath string) {
	if selectPath == "" {
		if e, ok := a.current(); ok {
			selectPath = e.Path
		}
	}
	a.parents = map[*tview.TreeNode]*tview.TreeNode{}
	root := a.newNode(Entry{Name: filepath.Base(a.root), Path: a.root, Kind: KindDir})
	a.fill(root)
	root.SetExpanded(true)
	a.tree.SetRoot(root)

	target := root
	root.Walk(func(n, _ *tview.TreeNode) bool {
		if ref, ok := n.GetReference().(*Entry); ok && ref.Path == selectPath {
			target = n
			return false
		}
		return true
	})
	// Walk only visits expanded nodes in a useful way; open the parents of the target.
	for p := a.parents[target]; p != nil; p = a.parents[p] {
		p.SetExpanded(true)
	}
	a.tree.SetCurrentNode(target)
	a.refreshPreview()
}

func (a *App) current() (Entry, bool) {
	n := a.tree.GetCurrentNode()
	if n == nil {
		return Entry{}, false
	}
	e, ok := n.GetReference().(*Entry)
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// targetDir is the directory a new file goes into: the selected folder, or the
// folder of the selected file.
func (a *App) targetDir() string {
	e, ok := a.current()
	switch {
	case !ok:
		return a.root
	case e.Kind == KindDir:
		return e.Path
	}
	return filepath.Dir(e.Path)
}

func (a *App) onChanged(n *tview.TreeNode) {
	if e, ok := n.GetReference().(*Entry); ok && a.session != nil && a.session.Path != e.Path {
		a.lock() // moving to another file locks the one you were reading
	}
	a.refreshPreview()
}

func (a *App) onEnter(n *tview.TreeNode) {
	e, ok := n.GetReference().(*Entry)
	if !ok {
		return
	}
	switch e.Kind {
	case KindDir:
		if n.IsExpanded() {
			n.Collapse()
			delete(a.expanded, e.Path)
		} else {
			a.fill(n)
			n.Expand()
			a.expanded[e.Path] = true
		}
	case KindSedit:
		a.unlockCurrent(func(s *Session) { a.showSession(s) })
	default:
		a.refreshPreview()
	}
}

// ---- the preview and the status line ------------------------------------

func (a *App) refreshPreview() {
	e, ok := a.current()
	switch {
	case !ok:
		a.prev.showMessage("")
		a.prev.SetTitle(" Preview ")
	case a.session != nil && !a.session.Locked() && a.session.Path == e.Path:
		a.showSession(a.session)
	case e.Kind == KindSedit:
		a.prev.SetTitle(" " + e.Name + "  (locked) ")
		a.prev.showMessage("\nLOCKED\n\nPress Enter to unlock.\nPress e to edit.")
	case e.Kind == KindAge:
		a.prev.SetTitle(" " + e.Name + " ")
		a.prev.showMessage("\nAn age-encrypted file.\n\nTo turn it into a sedit file:\n  sedit --import " + e.Name + " -i KEY")
	case e.Kind == KindDir:
		a.prev.SetTitle(" " + e.Name + "/ ")
		n := 0
		if es, err := List(e.Path); err == nil {
			n = len(es)
		}
		a.prev.showMessage(fmt.Sprintf("\n%d items.\n\nEnter opens or closes the folder.\nn creates a new file in it.", n))
	default:
		a.prev.SetTitle(" " + e.Name + " ")
		a.prev.showMessage("\nNot a sedit file.\n\nTo encrypt it in place:\n  sedit --encrypt " + e.Name)
	}
}

func (a *App) showSession(s *Session) {
	a.prev.showText(s.Plain)
	a.updateTitle()
}

func (a *App) updateTitle() {
	if a.session == nil || a.session.Locked() {
		return
	}
	left := a.opts.Idle - time.Since(a.last)
	if left < 0 {
		left = 0
	}
	a.prev.SetTitle(fmt.Sprintf(" %s  (unlocked, locks in %d:%02d) ", filepath.Base(a.session.Path), int(left.Minutes()), int(left.Seconds())%60))
}

// short shows paths inside the folder being browsed relative to it; the status
// line is one row, and a long absolute path would push the point of the message
// off the end.
func (a *App) short(msg string) string {
	return strings.ReplaceAll(msg, a.root+string(filepath.Separator), "")
}

func (a *App) say(msg string, isErr bool) {
	a.msg, a.msgErr = a.short(msg), isErr
	a.renderStatus()
}

const hints = "↑↓ move  ⏎ unlock  e edit  n new  r rename  d delete  p password  l lock  Tab scroll  ? help  q quit"

func (a *App) renderStatus() {
	switch {
	case a.msg == "":
		a.status.SetText("[gray]" + tview.Escape(hints))
	case a.msgErr:
		a.status.SetText("[red]" + tview.Escape(a.msg))
	default:
		a.status.SetText("[green]" + tview.Escape(a.msg))
	}
}

// ---- locking ------------------------------------------------------------

// lock wipes the unlocked file from memory and shows the locked view again.
func (a *App) lock() {
	if a.session != nil {
		a.session.Wipe()
		a.session = nil
	}
	if a.tree != nil && a.tree.GetCurrentNode() != nil {
		a.refreshPreview()
	}
}

// tick runs every second: it counts down the idle timer.
func (a *App) tick() {
	if a.session == nil || a.session.Locked() {
		return
	}
	if time.Since(a.last) >= a.opts.Idle {
		name := filepath.Base(a.session.Path)
		a.lock()
		a.say(fmt.Sprintf("%s was locked after %s without activity", name, a.opts.Idle), false)
		return
	}
	a.updateTitle()
}

// ---- keys ---------------------------------------------------------------

func (a *App) inMain() bool {
	name, _ := a.pages.GetFrontPage()
	return name == "main"
}

func (a *App) capture(ev *tcell.EventKey) *tcell.EventKey {
	a.last = time.Now()
	if a.msg != "" {
		a.msg = ""
		a.renderStatus()
	}
	if !a.inMain() {
		return ev // a dialog is open: it handles its own keys
	}
	if a.app.GetFocus() == a.prev {
		if ev.Key() == tcell.KeyEscape || ev.Key() == tcell.KeyTab {
			a.app.SetFocus(a.tree)
			return nil
		}
		return ev
	}
	switch ev.Key() {
	case tcell.KeyTab:
		a.app.SetFocus(a.prev)
		return nil
	case tcell.KeyHome:
		a.selectEdge(true)
		return nil
	case tcell.KeyEnd:
		a.selectEdge(false)
		return nil
	case tcell.KeyRight:
		a.expandCurrent(true)
		return nil
	case tcell.KeyLeft:
		a.expandCurrent(false)
		return nil
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'g':
			a.selectEdge(true)
		case 'G':
			a.selectEdge(false)
		case 'q':
			a.quit()
		case '?':
			a.help()
		case 'e':
			a.editCurrent()
		case 'n':
			a.newFile()
		case 'r':
			a.rename()
		case 'd':
			a.remove()
		case 'p':
			a.changePassword()
		case 'l':
			a.lock()
			a.say("locked", false)
		case 'R':
			a.reload("")
		default:
			return ev
		}
		return nil
	}
	return ev
}

// expandCurrent opens (or, with open false, closes or leaves) the selected folder.
func (a *App) expandCurrent(open bool) {
	n := a.tree.GetCurrentNode()
	e, ok := n.GetReference().(*Entry)
	if !ok {
		return
	}
	switch {
	case open && e.Kind == KindDir && !n.IsExpanded():
		a.fill(n)
		n.Expand()
		a.expanded[e.Path] = true
	case !open && e.Kind == KindDir && n.IsExpanded() && n != a.tree.GetRoot():
		n.Collapse()
		delete(a.expanded, e.Path)
	case !open:
		if p := a.parents[n]; p != nil {
			a.tree.SetCurrentNode(p)
		}
	}
}

// selectEdge selects the first or last visible entry. (The tree widget's own
// Home and End only scroll the view.)
func (a *App) selectEdge(first bool) {
	var visible []*tview.TreeNode
	a.tree.GetRoot().Walk(func(n, _ *tview.TreeNode) bool {
		visible = append(visible, n)
		return n.IsExpanded() // don't go into closed folders
	})
	if len(visible) == 0 {
		return
	}
	if first {
		a.tree.SetCurrentNode(visible[0])
	} else {
		a.tree.SetCurrentNode(visible[len(visible)-1])
	}
	a.onChanged(a.tree.GetCurrentNode())
}

func (a *App) quit() {
	a.lock()
	a.app.Stop()
}

// ---- dialogs ------------------------------------------------------------

func center(p tview.Primitive, w, h int) tview.Primitive {
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(p, h, 0, true).
			AddItem(nil, 0, 1, false), w, 0, true).
		AddItem(nil, 0, 1, false)
}

func (a *App) openDialog(name string, p tview.Primitive, w, h int) {
	a.pages.AddPage(name, center(p, w, h), true, true)
	a.app.SetFocus(p)
}

func (a *App) closeDialog(name string) {
	a.pages.RemovePage(name)
	a.app.SetFocus(a.tree)
}

// secretDialog asks for a password. done gets it (and owns it), or ok=false.
func (a *App) secretDialog(title, label, errMsg string, done func(pw []byte, ok bool)) {
	box := newSecretBox(title, label, errMsg, func(pw []byte, ok bool) {
		a.closeDialog("secret")
		done(pw, ok)
	})
	a.openDialog("secret", box, 56, 6)
}

// inputDialog asks for a line of text.
func (a *App) inputDialog(title, label, initial string, done func(text string, ok bool)) {
	in := tview.NewInputField().SetLabel(label + " ").SetText(initial).SetFieldWidth(0)
	box := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(in, 1, 0, true).
		AddItem(tview.NewTextView().SetText("Enter to confirm, Esc to cancel").SetTextColor(tcell.ColorGray), 1, 0, false)
	box.SetBorder(true).SetTitle(" "+title+" ").SetBorderPadding(1, 0, 1, 1)
	in.SetDoneFunc(func(key tcell.Key) {
		text := in.GetText()
		switch key {
		case tcell.KeyEnter:
			a.closeDialog("input")
			done(text, true)
		case tcell.KeyEscape:
			a.closeDialog("input")
			done("", false)
		}
	})
	a.openDialog("input", box, 64, 6)
}

// choiceDialog shows a question with buttons; done gets the index of the one
// pressed, or -1 for Esc. The first button has the focus, so put the safe one first.
func (a *App) choiceDialog(text string, buttons []string, done func(index int)) {
	m := tview.NewModal().SetText(text).AddButtons(buttons)
	m.SetDoneFunc(func(i int, _ string) {
		a.closeDialog("choice")
		done(i)
	})
	a.pages.AddPage("choice", m, true, true)
	a.app.SetFocus(m)
}

// textDialog shows text in a box that closes on Enter, Esc or q.
func (a *App) textDialog(name, title, text string, w, h int) {
	tv := tview.NewTextView().SetText(text).SetWrap(false)
	tv.SetBorder(true).SetTitle(" "+title+" ").SetBorderPadding(0, 0, 1, 1)
	tv.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEnter || ev.Key() == tcell.KeyEscape || (ev.Key() == tcell.KeyRune && ev.Rune() == 'q') {
			a.closeDialog(name)
			return nil
		}
		return ev
	})
	a.openDialog(name, tv, w, h)
}

func (a *App) help() {
	a.textDialog("help", "Keys", strings.Join([]string{
		"Files are listed on the left; the right shows the selected one.",
		"Nothing is decrypted until you press Enter.",
		"",
		"↑ ↓      move          → ←   open / close a folder",
		"Enter    unlock and show the file",
		"e        edit it in your editor (unlocks first)",
		"n        new file      r     rename or move      d  delete",
		"p        change its password",
		"l        lock now      R     re-read the folder",
		"Tab      scroll the preview (Esc or Tab comes back)",
		"q        quit",
		"",
		"A file locks again when you move to another one, or after",
		fmt.Sprintf("%s without a key press.", a.opts.Idle),
		"",
		"▪ sedit file    ○ age file    · other file",
		"",
		"Enter, Esc or q closes this box.",
	}, "\n"), 72, 22)
}

// ---- actions ------------------------------------------------------------

// unlockCurrent makes the selected sedit file the unlocked one, using a stored
// password if there is one and asking otherwise, then calls then.
func (a *App) unlockCurrent(then func(*Session)) {
	e, ok := a.current()
	if !ok || e.Kind != KindSedit {
		a.say("select a sedit file first", true)
		return
	}
	if a.session != nil && !a.session.Locked() && a.session.Path == e.Path {
		then(a.session)
		return
	}
	a.lock()
	s, err := OpenSession(e.Path)
	if err != nil {
		a.say(err.Error(), true)
		return
	}
	ok, err = s.TryStored(func(m string) { a.say(m, false) })
	switch {
	case err != nil:
		a.say(err.Error(), true)
	case ok:
		a.session = s
		then(s)
	default:
		a.askPassword(s, "", then)
	}
}

func (a *App) askPassword(s *Session, errMsg string, then func(*Session)) {
	a.secretDialog("Unlock "+filepath.Base(s.Path), "Password:", errMsg, func(pw []byte, ok bool) {
		if !ok {
			return
		}
		if err := s.UnlockWith(pw); err != nil {
			vault.Wipe(pw)
			if errors.Is(err, vault.ErrBadPassword) {
				a.askPassword(s, "Wrong password, try again", then)
				return
			}
			a.say(err.Error(), true)
			return
		}
		a.session = s
		then(s)
	})
}

func (a *App) editCurrent() {
	a.unlockCurrent(func(s *Session) { a.runEditor(s, "") })
}

// runEditor suspends the interface, runs the editor on s, and reports. selectAfter
// is the path to select afterwards when the file is new.
func (a *App) runEditor(s *Session, selectAfter string) {
	var saved bool
	var err error
	a.app.Suspend(func() { saved, err = s.Edit() })
	a.last = time.Now()
	name := filepath.Base(s.Path)
	switch {
	case err != nil:
		a.say(err.Error(), true)
	case saved:
		a.say("saved "+name+" (encrypted)", false)
	default:
		a.say("no changes to "+name, false)
	}
	if selectAfter != "" {
		if _, statErr := os.Stat(selectAfter); statErr != nil { // the new file wasn't created
			s.Wipe()
			a.session = nil
			a.reload("")
			return
		}
		a.session = s
		a.reload(selectAfter)
		return
	}
	a.reload("")
}

// newFile asks for a name and a password, then opens the editor on an empty file.
func (a *App) newFile() {
	dir := a.targetDir()
	a.inputDialog("New file in "+filepath.Base(dir), "Name:", "", func(name string, ok bool) {
		name = strings.TrimSpace(name)
		switch {
		case !ok || name == "":
			return
		case name != filepath.Base(name) || name == "." || name == "..":
			a.say("give a file name, not a path (move it afterwards with r)", true)
			return
		case strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".bak"):
			a.say("names starting with . or ending in .bak are hidden from the list", true)
			return
		}
		path := filepath.Join(dir, name)
		if _, err := os.Lstat(path); err == nil {
			a.say(name+" already exists", true)
			return
		}
		a.choosePassword(func(pw []byte, ok bool) {
			if !ok {
				return
			}
			a.lock()
			a.runEditor(NewSession(path, pw), path)
		})
	})
}

func (a *App) rename() {
	e, ok := a.current()
	if !ok || e.Kind != KindSedit {
		a.say("select a sedit file to rename", true)
		return
	}
	a.inputDialog("Rename or move", "New name or path:", e.Name, func(to string, ok bool) {
		to = strings.TrimSpace(to)
		if !ok || to == "" || to == e.Name {
			return
		}
		if !filepath.IsAbs(to) {
			to = filepath.Join(filepath.Dir(e.Path), to)
		}
		a.lock()
		dest, res, err := fileops.Move(e.Path, to)
		a.reportOps(res)
		if err != nil {
			a.say(err.Error(), true)
			return
		}
		a.reload(dest)
		a.say("moved to "+dest+alsoText(res), false)
	})
}

func (a *App) remove() {
	e, ok := a.current()
	if !ok || e.Kind != KindSedit {
		a.say("select a sedit file to delete", true)
		return
	}
	a.choiceDialog(fmt.Sprintf("Delete %s?\n\nIts backup and stored password\nare deleted too.\n\nThis can't be undone, and it is\nnot a secure erase: the encrypted\ndata may remain in backups.", e.Name),
		[]string{"Cancel", "Delete"}, func(i int) {
			if i != 1 {
				return
			}
			a.lock()
			res, err := fileops.Remove(e.Path)
			a.reportOps(res)
			if err != nil {
				a.say(err.Error(), true)
				return
			}
			a.reload("")
			a.say("deleted "+e.Name+alsoText(res), false)
		})
}

func (a *App) changePassword() {
	a.unlockCurrent(func(s *Session) {
		a.choosePassword(func(pw []byte, ok bool) {
			if !ok {
				return
			}
			res, err := s.Rekey(pw)
			var storeErr *fileops.StoreUpdateError
			switch {
			case err != nil && !errors.As(err, &storeErr):
				vault.Wipe(pw)
				a.say(err.Error(), true)
			case err != nil:
				a.say(err.Error(), true) // changed, but the Keychain entry is stale
			default:
				a.reportOps(res)
				a.say("password changed for "+filepath.Base(s.Path), false)
			}
		})
	})
}

// choosePassword picks the password for a new file or a new password: the
// default, if there is one and the user wants it, or one typed twice.
func (a *App) choosePassword(done func(pw []byte, ok bool)) {
	if pw, err := keystore.GetDefault(); err == nil {
		vault.Wipe(pw)
		a.choiceDialog("Which password should be used?", []string{"Custom password", "Default password", "Cancel"}, func(i int) {
			switch i {
			case 0:
				a.askNewPassword("", done)
			case 1:
				pw, err := keystore.GetDefault()
				if err != nil {
					a.say(err.Error(), true)
					done(nil, false)
					return
				}
				done(pw, true)
			default:
				done(nil, false)
			}
		})
		return
	}
	a.askNewPassword("", done)
}

func (a *App) askNewPassword(errMsg string, done func(pw []byte, ok bool)) {
	a.secretDialog("New password", "Choose a password:", errMsg, func(p1 []byte, ok bool) {
		if !ok {
			done(nil, false)
			return
		}
		if len(p1) == 0 {
			a.askNewPassword("An empty password is not allowed", done)
			return
		}
		a.secretDialog("New password", "Type it again:", "", func(p2 []byte, ok bool) {
			if !ok {
				vault.Wipe(p1)
				done(nil, false)
				return
			}
			same := bytes.Equal(p1, p2)
			vault.Wipe(p2)
			if !same {
				vault.Wipe(p1)
				a.askNewPassword("The passwords didn't match, try again", done)
				return
			}
			done(p1, true)
		})
	})
}

// ---- messages from file operations --------------------------------------

func alsoText(res fileops.Result) string {
	var parts []string
	if res.Backup {
		parts = append(parts, "its backup")
	}
	if res.Entry {
		parts = append(parts, "its stored password")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (and " + strings.Join(parts, " and ") + ")"
}

// reportOps shows warnings from a file operation. They are shown in the status
// line, so only the last one stays visible; the operation's own message follows.
func (a *App) reportOps(res fileops.Result) {
	for _, w := range res.Warnings {
		a.say("warning: "+w, true)
	}
}
