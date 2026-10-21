package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseUIDecidesBetweenTheInterfaceAndTheCommandLine(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.txt")
	os.WriteFile(file, []byte("x"), 0o600)

	for _, c := range []struct {
		args []string
		ui   bool
		dir  string
	}{
		{nil, true, ""},                         // bare sedit
		{[]string{dir}, true, dir},              // a folder
		{[]string{"--idle", "5m"}, true, ""},    // only its own options
		{[]string{"--idle=5m", dir}, true, dir}, // = form, folder after
		{[]string{dir, "--clipboard-timeout", "10s"}, true, dir},
		{[]string{file}, false, ""},                          // a file is the command line's
		{[]string{filepath.Join(dir, "new.txt")}, false, ""}, // a file to create
		{[]string{"-p", file}, false, ""},
		{[]string{"--passwd", file}, false, ""},
		{[]string{"-h"}, false, ""},
		{[]string{"--version"}, false, ""},
		{[]string{"--mv", dir, dir + "2"}, false, ""}, // a mode flag wins over a folder
		{[]string{dir, file}, false, ""},              // two arguments: not "sedit DIR"
	} {
		o, err := parseUI(c.args)
		if err != nil {
			t.Errorf("%v: %v", c.args, err)
			continue
		}
		if (o != nil) != c.ui {
			t.Errorf("%v: interface=%v, want %v", c.args, o != nil, c.ui)
			continue
		}
		if o != nil && o.dir != c.dir {
			t.Errorf("%v: dir %q, want %q", c.args, o.dir, c.dir)
		}
	}
}

func TestParseUIOptions(t *testing.T) {
	o, err := parseUI([]string{"--idle", "5m", "--clipboard-timeout=10s"})
	if err != nil || o == nil {
		t.Fatal(o, err)
	}
	if o.idle != 5*time.Minute || o.clip != 10*time.Second {
		t.Errorf("idle %v, clip %v", o.idle, o.clip)
	}
	o, _ = parseUI(nil)
	if o.idle != 2*time.Minute || o.clip != 30*time.Second {
		t.Errorf("defaults: idle %v, clip %v", o.idle, o.clip)
	}
}

func TestParseUIRefusals(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.txt")
	os.WriteFile(file, []byte("x"), 0o600)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--idle"}, "needs a value"},
		{[]string{"--idle", "soon"}, "not a duration"},
		{[]string{"--idle", "0s"}, "at least 1s"},
		{[]string{"--clipboard-timeout=500ms"}, "at least 1s"},
		{[]string{"--idle", "5m", file}, "for the interface"},       // they don't apply to a file
		{[]string{"--idle", "5m", "-p", file}, "for the interface"}, // nor to a mode
	} {
		if _, err := parseUI(c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.args, err, c.want)
		}
	}
}

func TestUIDir(t *testing.T) {
	home := t.TempDir()
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "SEDIT_HOME" {
				return v
			}
			return ""
		}
	}
	other := t.TempDir()
	if got, err := uiDir(other, env(home)); err != nil || got != other {
		t.Errorf("a folder given wins over SEDIT_HOME: %q, %v", got, err)
	}
	if got, err := uiDir("", env(home)); err != nil || got != home {
		t.Errorf("SEDIT_HOME: %q, %v", got, err)
	}
	if got, err := uiDir("", env("")); err != nil || got != "." {
		t.Errorf("neither: %q, %v", got, err)
	}
	if _, err := uiDir("", env(filepath.Join(home, "missing"))); err == nil || !strings.Contains(err.Error(), "SEDIT_HOME=") {
		t.Errorf("a missing SEDIT_HOME: %v", err)
	}
	f := filepath.Join(home, "file")
	os.WriteFile(f, nil, 0o600)
	if _, err := uiDir("", env(f)); err == nil {
		t.Error("SEDIT_HOME naming a file was accepted")
	}
	if uh, err := os.UserHomeDir(); err == nil {
		if got, err := uiDir("", env("~")); err != nil || got != uh {
			t.Errorf("~: %q, %v (want %q)", got, err, uh)
		}
	}
}

func TestTheInterfaceNeedsATerminal(t *testing.T) {
	old := isTerminal
	isTerminal = func() bool { return false }
	defer func() { isTerminal = old }()
	err := run(nil)
	if err == nil || !strings.Contains(err.Error(), "needs a terminal") {
		t.Errorf("got %v", err)
	}
	// The command line is not affected.
	if err := run([]string{"--version"}); err != nil {
		t.Error(err)
	}
}

func TestACommandLineErrorStillSaysSo(t *testing.T) {
	old := isTerminal
	isTerminal = func() bool { return false }
	defer func() { isTerminal = old }()
	err := run([]string{"--no-such-option"})
	if err == nil || !strings.Contains(err.Error(), "unknown option") {
		t.Errorf("got %v", err)
	}
}
