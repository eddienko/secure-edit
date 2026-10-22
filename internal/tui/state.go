package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// What the list looked like when sedit was last closed, so that it opens the
// same way: the folders that were open, the entry that was selected and the
// layout. It is kept per top folder in Options.StateFile (nothing if that is
// empty). It holds folder and file names, never anything from inside a file.

// viewState is the remembered view of one top folder.
type viewState struct {
	Open     []string `json:"open,omitempty"`     // open folders, relative to the top folder
	Selected string   `json:"selected,omitempty"` // relative to the top folder; empty for the top folder
	TwoLine  bool     `json:"two_line,omitempty"`
}

// readStates reads the whole state file; a missing or damaged one is empty.
func readStates(path string) map[string]viewState {
	all := map[string]viewState{}
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &all) != nil || all == nil {
			all = map[string]viewState{}
		}
	}
	return all
}

// restoreView applies the remembered view of a.root, if there is one.
func (a *App) restoreView() (selected string) {
	if a.opts.StateFile == "" {
		return ""
	}
	st, ok := readStates(a.opts.StateFile)[a.root]
	if !ok {
		return ""
	}
	for _, rel := range st.Open {
		if p := filepath.Join(a.root, rel); inside(a.root, p) && isDirPath(p) {
			a.expanded[p] = true
		}
	}
	if !a.opts.TwoLine {
		a.twoLine = st.TwoLine
	}
	if st.Selected != "" {
		if p := filepath.Join(a.root, st.Selected); inside(a.root, p) {
			if _, err := os.Lstat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// saveView writes the current view of a.root, leaving other folders' alone.
// Failing to write it is not worth bothering the user about.
func (a *App) saveView() {
	if a.opts.StateFile == "" {
		return
	}
	st := viewState{TwoLine: a.twoLine}
	for p := range a.expanded {
		if p != a.root && inside(a.root, p) && isDirPath(p) {
			rel, _ := filepath.Rel(a.root, p)
			st.Open = append(st.Open, rel)
		}
	}
	sort.Strings(st.Open)
	if e, ok := a.current(); ok && e.Path != a.root && inside(a.root, e.Path) {
		st.Selected, _ = filepath.Rel(a.root, e.Path)
	}
	all := readStates(a.opts.StateFile)
	all[a.root] = st
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil || os.MkdirAll(filepath.Dir(a.opts.StateFile), 0o700) != nil {
		return
	}
	tmp := a.opts.StateFile + ".tmp"
	if os.WriteFile(tmp, data, 0o600) != nil {
		return
	}
	if os.Rename(tmp, a.opts.StateFile) != nil {
		os.Remove(tmp)
	}
}

func inside(root, p string) bool { return strings.HasPrefix(p, root+string(filepath.Separator)) }

func isDirPath(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
