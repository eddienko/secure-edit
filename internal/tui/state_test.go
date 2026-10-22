package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestTheViewIsRememberedBetweenRuns(t *testing.T) {
	useStore(t)
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(deep, "n.txt")
	os.WriteFile(file, []byte("x"), 0o600)
	os.MkdirAll(filepath.Join(root, "other"), 0o700)
	state := filepath.Join(t.TempDir(), "cfg", "state.json")

	open := func() *App {
		a, err := New(root, Options{Screen: tcell.NewSimulationScreen("UTF-8"), StateFile: state})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := open()
	if a.expanded[deep] {
		t.Fatal("a first run opens nothing")
	}
	a.expanded[filepath.Join(root, "a")], a.expanded[deep] = true, true
	a.twoLine = true
	a.reload(file)
	a.saveView()

	b := open()
	if !b.expanded[deep] || !b.expanded[filepath.Join(root, "a")] || b.expanded[filepath.Join(root, "other")] {
		t.Errorf("open folders not restored: %v", b.expanded)
	}
	if e, ok := b.current(); !ok || e.Path != file {
		t.Errorf("selection not restored: %v", e.Path)
	}
	if !b.twoLine {
		t.Error("the layout was not restored")
	}
	if fi, err := os.Stat(state); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("state file: %v %v", fi, err)
	}

	// A folder or file that is gone is simply not there; another root is untouched.
	os.RemoveAll(filepath.Join(root, "a"))
	if c := open(); c.expanded[deep] {
		t.Error("a removed folder was restored")
	}
	other := t.TempDir()
	o, _ := New(other, Options{Screen: tcell.NewSimulationScreen("UTF-8"), StateFile: state})
	o.saveView()
	if len(readStates(state)) != 2 {
		t.Errorf("states of other folders were lost: %v", readStates(state))
	}
}

func TestNoStateFileMeansNothingIsKept(t *testing.T) {
	useStore(t)
	a, err := New(t.TempDir(), Options{Screen: tcell.NewSimulationScreen("UTF-8")})
	if err != nil {
		t.Fatal(err)
	}
	a.saveView() // must not write anywhere
}
