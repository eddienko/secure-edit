package tui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"

	"github.com/eddienko/secure-edit/internal/clip"
	"github.com/eddienko/secure-edit/internal/fileops"
	"github.com/eddienko/secure-edit/internal/keystore"
	"github.com/eddienko/secure-edit/internal/vault"
)

// DefaultIdle is how long an unlocked file stays unlocked without any key press.
const DefaultIdle = 2 * time.Minute

// DefaultClipboardTTL is how long copied text stays on the clipboard.
const DefaultClipboardTTL = 30 * time.Second

// Options configures an App. The zero value is fine for real use.
type Options struct {
	Idle      time.Duration // auto-lock after this long without input; DefaultIdle if zero
	TickEvery time.Duration // how often the idle timer is checked; one second if zero
	Screen    tcell.Screen  // for tests: a simulated screen

	ClipboardTTL time.Duration  // how long copied text stays on the clipboard; DefaultClipboardTTL if zero
	Clipboard    clip.Clipboard // for tests: a fake clipboard; the system one if nil
}

// App is the interface: a tree of files on the left, a locked preview on the
// right, and a status line.
type App struct {
	app    *tview.Application
	pages  *tview.Pages
	tree   *tview.TreeView
	prev   *preview
	status *statusBar

	root string
	opts Options

	session  *Session // the unlocked file, if any; at most one
	expanded map[string]bool
	parents  map[*tview.TreeNode]*tview.TreeNode
	last     time.Time // last key press
	msg      string
	msgErr   bool

	filter        string          // what the list is narrowed to; empty for the whole list
	filterEditing bool            // the filter is being typed
	index         []Entry         // everything under root, while filtering
	visible       map[string]bool // paths shown while filtering: the matches and the folders leading to them
	matchCount    int

	busy    bool         // a folder is being moved in the background: keys are ignored
	quiet   bool         // a file is open in memory but the preview still shows it locked (see unlockFor)
	holder  *clip.Holder // copies to the clipboard and clears it again; made on first use
	stopped atomic.Bool  // set when the interface has closed
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
	if opts.ClipboardTTL <= 0 {
		opts.ClipboardTTL = DefaultClipboardTTL
	}
	useTerminalColors() // before any widget is made: they read the theme when created
	a := &App{
		app:      tview.NewApplication(),
		pages:    tview.NewPages(),
		prev:     newPreview(),
		status:   newStatusBar(),
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
	a.status.hints = a.hintsFor
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
	a.stopped.Store(true)
	a.lock() // never leave secrets behind
	a.releaseClipboard()
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

// label is how an entry appears in the list. Names and titles come from the
// disk, so they are escaped: tview would take a "[red]" in them for a style.
// A file with a title is listed by it, with the file's name after it in dim
// text so that it can still be told apart on disk; "dim" is an attribute, so it
// follows the terminal's colours like everything else.
func (a *App) label(e Entry) (string, tcell.Style) {
	name := tview.Escape(safeText(e.Name, false))
	switch e.Kind {
	case KindDir:
		if title := safeText(e.Meta.Title, false); title != "" {
			return tview.Escape(title) + "  [::d]" + name + "/[::D]", bold
		}
		return name + "/", bold
	case KindSedit:
		if title := safeText(e.Meta.Title, false); title != "" {
			return "▪ " + tview.Escape(title) + "  [::d]" + name + "[::D]", plain
		}
		return "▪ " + name, plain
	case KindAge:
		return "○ " + name, plain
	}
	return "· " + name, plain
}

func (a *App) newNode(e Entry) *tview.TreeNode {
	text, style := a.label(e)
	n := tview.NewTreeNode(text).SetReference(&e).SetTextStyle(style).SetSelectedTextStyle(invert).SetSelectable(true)
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
		if a.filtering() && !a.visible[c.Path] {
			continue
		}
		child := a.newNode(c)
		a.parents[child] = n
		n.AddChild(child)
		if c.Kind == KindDir && (a.filtering() || a.expanded[c.Path]) {
			a.fill(child)
			child.SetExpanded(true)
		}
	}
}

// reload rebuilds the tree from disk, keeping the folders that were open, and
// selects selectPath if it is there (otherwise the previous selection, if any).
func (a *App) reload(selectPath string) {
	a.index = nil // the disk may have changed
	a.rebuild(selectPath)
}

// rebuild is reload without reading the disk again for the filter.
func (a *App) rebuild(selectPath string) {
	explicit := selectPath != ""
	if selectPath == "" {
		if e, ok := a.current(); ok {
			selectPath = e.Path
		}
	}
	a.computeVisible()
	// A file that was just made or moved must not be hidden by the filter.
	if a.filtering() && selectPath != "" && !a.visible[selectPath] && explicit {
		a.filter, a.visible, a.matchCount, a.filterEditing = "", nil, 0, false
	}
	// A file or folder that was just made or moved must be in view: open the
	// folders that lead to it (the filter may have been opening them for us).
	if explicit {
		for p := filepath.Dir(selectPath); strings.HasPrefix(p, a.root) && p != a.root; p = filepath.Dir(p) {
			a.expanded[p] = true
		}
	}
	a.parents = map[*tview.TreeNode]*tview.TreeNode{}
	root := a.newNode(Entry{Name: filepath.Base(a.root), Path: a.root, Kind: KindDir})
	a.fill(root)
	root.SetExpanded(true)
	a.tree.SetRoot(root)

	target := root
	found := false
	root.Walk(func(n, _ *tview.TreeNode) bool {
		if ref, ok := n.GetReference().(*Entry); ok && ref.Path == selectPath {
			target, found = n, true
			return false
		}
		return true
	})
	// When the selection has been filtered away, or is only the top folder, take
	// the first match.
	if (!found || target == root) && a.filtering() {
		first := true // Walk goes on after false, so only the first one is taken
		root.Walk(func(n, _ *tview.TreeNode) bool {
			if ref, ok := n.GetReference().(*Entry); ok && ref.Kind != KindDir && first {
				target, first = n, false
			}
			return true
		})
	}
	// Walk only visits expanded nodes in a useful way; open the parents of the target.
	for p := a.parents[target]; p != nil; p = a.parents[p] {
		p.SetExpanded(true)
	}
	a.tree.SetCurrentNode(target)
	a.updateTreeTitle()
	a.refreshPreview()
}

// updateTreeTitle puts the filter, if there is one, in the list's title.
func (a *App) updateTreeTitle() {
	title := treeTitle(a.root)
	if a.filtering() {
		title = trimTo(title, 14) + " · /" + a.filter
	}
	a.tree.SetTitle(" " + tview.Escape(title) + " ")
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
		a.prev.SetTitle(a.paneTitle(e.Meta.Title, e.Name, "(locked)"))
		a.prev.showLocked(e.Meta)
	case e.Kind == KindAge:
		a.prev.SetTitle(" " + e.Name + " ")
		a.prev.showMessage("\nAn age-encrypted file.\n\nTo turn it into a sedit file:\n  sedit --import " + e.Name + " -i KEY")
	case e.Kind == KindDir && a.filtering() && a.matchCount == 0:
		a.prev.SetTitle(" No match ")
		a.prev.showMessage("\nNothing matches \u201c" + safeText(a.filter, false) + "\u201d.\n\nThe filter looks at titles and names.\nEsc clears it; keep typing to change it.")
	case e.Kind == KindDir:
		a.showFolder(e)
	default:
		a.prev.SetTitle(" " + e.Name + " ")
		a.prev.showMessage("\nNot a sedit file.\n\nTo encrypt it in place:\n  sedit --encrypt " + e.Name)
	}
}

func (a *App) showSession(s *Session) {
	a.prev.showText(s.Plain, s.Meta().Description)
	a.updateTitle()
}

func (a *App) updateTitle() {
	if a.session == nil || a.session.Locked() || a.quiet {
		return
	}
	left := a.opts.Idle - time.Since(a.last)
	if left < 0 {
		left = 0
	}
	state := fmt.Sprintf("(unlocked, locks in %d:%02d)", int(left.Minutes()), int(left.Seconds())%60)
	a.prev.SetTitle(a.paneTitle(a.session.Meta().Title, filepath.Base(a.session.Path), state))
}

// paneTitle is the title of the preview for a sedit file: "Work VPN login ·
// vpn.txt  (state)", or "vpn.txt  (state)" if the file has no title. The title
// is cut short, with …, if the pane is too narrow for all of it, so that the
// file name and the state always show.
func (a *App) paneTitle(title, name, state string) string {
	label := tview.Escape(safeText(name, false))
	if title = safeText(title, false); title != "" {
		if room := a.prev.width - runewidth.StringWidth(" · "+name+"  "+state) - 2; a.prev.width > 0 && room >= 1 {
			title = trimTo(title, room)
		}
		label = tview.Escape(title) + " · " + label
	}
	if state == "" {
		return " " + label + " "
	}
	return " " + label + "  " + state + " "
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

// helpTail is always on the hint bar, at the end, whatever else has to go.
const helpTail = "? help  q quit"

// hintItems are the keys that matter right now, most useful first.
func (a *App) hintItems() []string {
	if a.prev.HasFocus() {
		return []string{"↑↓ scroll", "PgUp/PgDn page", "y copy", "Esc back"}
	}
	if a.filtering() {
		// The filter is on: how to change or drop it comes first.
		items := []string{"/ edit filter (" + plural(a.matchCount, "match", "matches") + ")", "Esc clear"}
		for _, it := range a.hintItemsFor() {
			if it != "/ filter" { // "/ edit filter" says it already
				items = append(items, it)
			}
		}
		return items
	}
	return a.hintItemsFor()
}

// hintItemsFor are the hints for what is selected.
func (a *App) hintItemsFor() []string {
	move := "↑↓ move"
	e, ok := a.current()
	switch {
	case !ok:
		return []string{"n new"}
	case e.Kind == KindDir && a.isTop(e):
		return []string{move, "→ open", "n new file", "N new folder", "/ filter", "i info", "R refresh"}
	case e.Kind == KindDir:
		return []string{move, "→ open", "n new file", "N new folder", "/ filter", "i info", "r rename", "m move", "d delete", "← close", "R refresh"}
	case e.Kind != KindSedit:
		return []string{move, "/ filter", "n new", "R refresh"}
	case a.session != nil && !a.session.Locked() && a.session.Path == e.Path:
		return []string{move, "e edit", "y copy", "J/K scroll", "l lock", "i info", "/ filter", "p password", "r rename", "m move", "d delete", "n new"}
	}
	return []string{move, "⏎ unlock", "e edit", "y copy", "i info", "/ filter", "n new", "r rename", "m move", "d delete", "p password"}
}

// hintsFor lays the hints out in width cells: as many as fit, in order, with
// the help and quit keys always last.
func (a *App) hintsFor(width int) string {
	if a.filterEditing {
		return a.filterLine()
	}
	text := helpTail
	room := width - runewidth.StringWidth(helpTail) - 2
	var items []string
	for _, it := range a.hintItems() {
		room -= runewidth.StringWidth(it) + 2
		if room < 0 {
			break
		}
		items = append(items, it)
	}
	if len(items) > 0 {
		text = strings.Join(items, "  ") + "  " + text
	}
	return text
}

func (a *App) renderStatus() {
	switch {
	case a.msg == "":
		a.status.set("", invert) // the bar shows hints for what is selected
	case a.msgErr:
		a.status.set(a.msg, alert)
	default:
		a.status.set(a.msg, invert) // same bar, so it replaces the hints in place
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
	if a.busy && ev.Key() != tcell.KeyCtrlC {
		return nil // wait for the move to finish
	}
	if a.msg != "" {
		a.msg = ""
		a.renderStatus()
	}
	if !a.inMain() {
		return ev // a dialog is open: it handles its own keys
	}
	if a.filterEditing {
		return a.filterKey(ev)
	}
	if a.app.GetFocus() == a.prev {
		if ev.Key() == tcell.KeyEscape || ev.Key() == tcell.KeyTab {
			a.app.SetFocus(a.tree)
			return nil
		}
		if ev.Key() == tcell.KeyRune && ev.Rune() == 'y' {
			a.copyCurrent()
			return nil
		}
		return ev
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		if a.filtering() {
			a.clearFilter()
			return nil
		}
		return ev
	case tcell.KeyTab:
		a.app.SetFocus(a.prev)
		return nil
	case tcell.KeyPgUp, tcell.KeyPgDn:
		if a.prev.showsText() { // the text is the thing to page through
			a.prev.scroll(map[bool]int{true: -1, false: 1}[ev.Key() == tcell.KeyPgUp] * a.prev.page())
			return nil
		}
		return ev
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
		case 'J', 'K', ' ':
			if !a.prev.showsText() {
				return ev
			}
			switch ev.Rune() {
			case 'J':
				a.prev.scroll(1)
			case 'K':
				a.prev.scroll(-1)
			default:
				a.prev.scroll(a.prev.page())
			}
		case 'g':
			a.selectEdge(true)
		case 'G':
			a.selectEdge(false)
		case 'q':
			a.quit()
		case '/':
			a.startFilter()
		case '?':
			a.help()
		case 'e':
			a.editCurrent()
		case 'y':
			a.copyCurrent()
		case 'i':
			a.editInfo()
		case 'n':
			a.newFile()
		case 'N':
			a.newFolder()
		case 'm':
			a.moveCurrent()
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
//
// The input field is itself the bordered box. It must not be wrapped in a
// tview.Flex: a Flex does not clear the area it covers (so that layouts don't
// wipe their children), which would let whatever is underneath show through
// the empty parts of the dialog.
func (a *App) inputDialog(title, label, initial string, done func(text string, ok bool)) {
	in := tview.NewInputField().SetLabel(label + " ").SetText(initial).SetFieldWidth(0)
	in.SetFieldStyle(tcell.StyleDefault.Underline(true)).SetLabelStyle(plain)
	in.SetBorder(true).SetTitle(" "+title+"  (Enter confirms, Esc cancels) ").SetBorderPadding(0, 0, 1, 1)
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
	a.openDialog("input", in, 72, 3)
}

// choiceDialog shows a question with buttons; done gets the index of the one
// pressed, or -1 for Esc. The first button has the focus, so put the safe one first.
func (a *App) choiceDialog(text string, buttons []string, done func(index int)) {
	// Buttons are drawn as [ label ], and the focused one in reverse video, so
	// they can be told apart without any colour.
	labels := make([]string, len(buttons))
	for i, b := range buttons {
		labels[i] = "[ " + b + " ]"
	}
	m := tview.NewModal().SetText(text).AddButtons(labels).
		SetButtonStyle(plain).SetButtonActivatedStyle(invert)
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
		fmt.Sprintf("y        copy the whole text to the clipboard (cleared after %s)", a.opts.ClipboardTTL),
		"i        change its title and description (they show while locked)",
		"/        filter the list by title or name (Enter keeps it, Esc clears)",
		"n        new file      N     new folder          m  move to a folder",
		"r        rename        d     delete (a folder only if empty)",
		"p        change its password",
		"l        lock now      R     re-read the folder",
		"J K      scroll the text a line     PgUp PgDn  a page     Space  a page down",
		"Tab      move into the text to scroll it (Esc or Tab comes back)",
		"q        quit",
		"",
		"A file locks again when you move to another one, or after",
		fmt.Sprintf("%s without a key press.", words(a.opts.Idle)),
		"",
		"▪ sedit file    ○ age file    · other file",
		"",
		"Enter, Esc or q closes this box.",
	}, "\n"), 78, 27)
}

// ---- actions ------------------------------------------------------------

// unlockCurrent makes the selected sedit file the unlocked one, using a stored
// password if there is one and asking otherwise, then calls then.
func (a *App) unlockCurrent(then func(*Session)) { a.unlockFor(true, then) }

// unlockFor is unlockCurrent, where reveal says whether the preview should show
// the text once the file is open. An action that needs the file open but has
// nothing to do with its text (changing the title, say) passes false, and puts
// the lock back itself when it is done.
func (a *App) unlockFor(reveal bool, then func(*Session)) {
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
		if reveal {
			a.showSession(s) // whatever happens next, the pane must not still say LOCKED
		}
		then(s)
	default:
		a.askPassword(s, "", reveal, then)
	}
}

// unlockHidden opens the selected file in memory for an action that has nothing
// to do with its text, without putting the text in the preview, which keeps
// saying LOCKED. then gets a finish function to call when it is done with the
// session, which locks the file again; if the file was already open on screen it
// is left that way.
func (a *App) unlockHidden(then func(s *Session, finish func())) {
	e, _ := a.current()
	wasShown := a.session != nil && !a.session.Locked() && a.session.Path == e.Path
	a.unlockFor(wasShown, func(s *Session) {
		a.quiet = !wasShown
		then(s, func() {
			if !wasShown {
				a.quiet = false
				a.lock()
			}
		})
	})
}

func (a *App) askPassword(s *Session, errMsg string, reveal bool, then func(*Session)) {
	a.secretDialog("Unlock "+filepath.Base(s.Path), "Password:", errMsg, func(pw []byte, ok bool) {
		if !ok {
			return
		}
		if err := s.UnlockWith(pw); err != nil {
			vault.Wipe(pw)
			if errors.Is(err, vault.ErrBadPassword) {
				a.askPassword(s, "Wrong password, try again", reveal, then)
				return
			}
			a.say(err.Error(), true)
			return
		}
		a.session = s
		if reveal {
			a.showSession(s)
		}
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

// copyCurrent copies the whole text of the selected file to the clipboard, and
// clears it again after the configured time (unless something else was copied
// by then). Like editing, it unlocks the file first if need be.
func (a *App) copyCurrent() {
	a.unlockCurrent(func(s *Session) {
		switch {
		case looksBinary(s.Plain):
			a.say("binary data can't be copied as text", true)
			return
		case len(s.Plain) == 0:
			a.say("the file is empty: nothing to copy", true)
			return
		}
		if a.holder == nil {
			c := a.opts.Clipboard
			if c == nil {
				var err error
				if c, err = clip.System(); err != nil {
					a.say(err.Error(), true)
					return
				}
			}
			a.holder = clip.NewHolder(c)
		}
		if err := a.holder.Copy(s.Plain, a.opts.ClipboardTTL, a.afterClear); err != nil {
			a.say("couldn't copy: "+err.Error(), true)
			return
		}
		a.say(fmt.Sprintf("copied %s to the clipboard; it is cleared in %s", filepath.Base(s.Path), a.opts.ClipboardTTL), false)
	})
}

// afterClear reports, from the timer's goroutine, what became of the clipboard.
func (a *App) afterClear(r clip.Result, err error) {
	if a.stopped.Load() {
		return
	}
	a.app.QueueUpdateDraw(func() {
		switch r {
		case clip.Cleared:
			a.say("clipboard cleared", false)
		case clip.LeftAlone:
			a.say("clipboard not cleared: it no longer held the copied text", false)
		default:
			a.say("couldn't clear the clipboard: "+err.Error(), true)
		}
	})
}

// releaseClipboard clears what we copied, now that the interface is closing:
// the timer would die with the process, leaving the text behind.
func (a *App) releaseClipboard() {
	if a.holder == nil {
		return
	}
	held, r, err := a.holder.ClearNow()
	switch {
	case !held:
	case err != nil:
		fmt.Fprintln(os.Stderr, "sedit: couldn't clear the clipboard:", err)
	case r == clip.Cleared:
		fmt.Fprintln(os.Stderr, "sedit: cleared the clipboard")
	case r == clip.LeftAlone:
		fmt.Fprintln(os.Stderr, "sedit: left the clipboard alone: it no longer held the copied text")
	}
}

// labelsHint is at the bottom of the dialogs that ask for a title and
// description.
const labelsHint = "Title and description are visible while locked: keep secrets out of them.\n" +
	"In the description Enter starts a new line; Ctrl-S saves from any field."

// descriptionLines is how tall the description box is. It scrolls.
const descriptionLines = 4

// newFile asks for a name, a title and a description in one box, then a password,
// then opens the editor on an empty file.
func (a *App) newFile() {
	dir := a.targetDir()
	a.fieldsDialog("newfile", "New file in "+filepath.Base(dir)+"  (Tab: next, Ctrl-S: create, Esc: cancel)", labelsHint,
		[]formField{{Label: "Name:"}, {Label: "Title:"}, {Label: "Description:", Lines: descriptionLines}},
		func(v []string) (int, error) {
			name := strings.TrimSpace(v[0])
			switch {
			case name == "":
				return 0, errors.New("give the file a name")
			case name != filepath.Base(name) || name == "." || name == "..":
				return 0, errors.New("give a file name, not a path (move it afterwards with r)")
			case strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".bak"):
				return 0, errors.New("names starting with . or ending in .bak are hidden from the list")
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
			path := filepath.Join(dir, strings.TrimSpace(v[0]))
			m := labelsFrom(v[1], v[2])
			a.choosePassword(func(pw []byte, ok bool) {
				if !ok {
					return
				}
				a.lock()
				s := NewSession(path, pw)
				s.NewMeta = m
				a.runEditor(s, path)
			})
		})
}

// labelsFrom makes a title and description from what was typed in the dialog.
func labelsFrom(title, description string) vault.Meta {
	return vault.Meta{Title: title, Description: description}.Clean()
}

// checkLabels says which of the title (field 1) and description (field 2) typed
// in a dialog is not acceptable, and why.
func checkLabels(title, description string) (int, error) {
	if err := (vault.Meta{Title: title}).Clean().Validate(); err != nil {
		return 1, err
	}
	if err := labelsFrom("", description).Validate(); err != nil {
		return 2, err
	}
	return 0, nil
}

// editInfo changes the title and description of the selected file. That asks for
// the password, like any change to the file.
func (a *App) editInfo() {
	if e, ok := a.current(); ok && e.Kind == KindDir {
		a.editFolderInfo(e) // no password: a folder's labels are not encrypted
		return
	}
	// Only the labels are being changed, so the preview keeps saying LOCKED
	// (and showing the description) rather than printing the text.
	a.unlockHidden(func(s *Session, finish func()) {
		old := s.Meta()
		name := filepath.Base(s.Path)
		a.fieldsDialog("info", "Title and description of "+name+"  (Ctrl-S: save, Esc: cancel)", labelsHint,
			[]formField{{Label: "Title:", Initial: old.Title}, {Label: "Description:", Initial: old.Description, Lines: descriptionLines}},
			func(v []string) (int, error) {
				i, err := checkLabels(v[0], v[1])
				return i - 1, err // this box has no Name field
			},
			func(v []string, ok bool) {
				var err error
				changed := false
				if ok {
					if m := labelsFrom(v[0], v[1]); m != old {
						err = s.SetMeta(m)
						changed = err == nil
					}
				}
				finish() // after SetMeta, which needs the file open
				switch {
				case !ok:
				case err != nil:
					a.say(err.Error(), true)
				case !changed:
					a.say("the title and description of "+name+" are unchanged", false)
				default:
					a.reload("")
					a.say("updated the title and description of "+name, false)
				}
			})
	})
}

func (a *App) rename() {
	e, ok := a.current()
	if ok && e.Kind == KindDir {
		a.renameFolder(e)
		return
	}
	if !ok || e.Kind != KindSedit {
		a.say("select a sedit file or a folder to rename", true)
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
	if ok && e.Kind == KindDir {
		a.removeFolder(e)
		return
	}
	if !ok || e.Kind != KindSedit {
		a.say("select a sedit file or an empty folder to delete", true)
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
	// The text isn't involved, so it is not put on screen.
	a.unlockHidden(func(s *Session, finish func()) {
		a.choosePassword(func(pw []byte, ok bool) {
			if !ok {
				finish()
				return
			}
			res, err := s.Rekey(pw)
			var storeErr *fileops.StoreUpdateError
			if err != nil && !errors.As(err, &storeErr) {
				vault.Wipe(pw) // the password did not change
			}
			finish()
			switch {
			case err != nil && !errors.As(err, &storeErr):
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
