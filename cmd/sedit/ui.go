package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/eddienko/secure-edit/internal/tui"
)

// uiOptions say how to open the interface.
type uiOptions struct {
	dir  string        // the folder given; empty to use $SEDIT_HOME or the current one
	idle time.Duration // lock an open file after this long without a key press
	clip time.Duration // clear copied text from the clipboard after this long
}

// parseUI decides whether args ask for the interface, and if so how. They do
// when there is nothing but its own options and at most one folder: a bare
// "sedit", "sedit ~/secrets", "sedit --idle 5m". Anything else (a file, any
// other option) is the command line's, so "sedit notes.txt" and "sedit -p FILE"
// are as they always were. It returns nil for the command line.
func parseUI(args []string) (*uiOptions, error) {
	o := &uiOptions{idle: tui.DefaultIdle, clip: tui.DefaultClipboardTTL}
	var positional []string
	sawOption, sawOther := false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, value, hasValue := strings.Cut(a, "=")
		switch name {
		case "--idle", "--clipboard-timeout":
			sawOption = true
			if !hasValue {
				if i+1 >= len(args) {
					return nil, errors.New(name + " needs a value, such as 5m or 30s")
				}
				i++
				value = args[i]
			}
			d, err := time.ParseDuration(value)
			if err != nil {
				return nil, fmt.Errorf("%s: %q is not a duration such as 5m or 30s", name, value)
			}
			if d < time.Second {
				return nil, fmt.Errorf("%s must be at least 1s", name)
			}
			if name == "--idle" {
				o.idle = d
			} else {
				o.clip = d
			}
		default:
			if strings.HasPrefix(a, "-") {
				sawOther = true
			} else {
				positional = append(positional, a)
			}
		}
	}

	wrong := errors.New("--idle and --clipboard-timeout are for the interface: sedit [--idle DURATION] [--clipboard-timeout DURATION] [DIR]")
	switch {
	case sawOther:
		if sawOption {
			return nil, wrong
		}
		return nil, nil
	case len(positional) == 0:
		return o, nil
	case len(positional) == 1 && isDir(positional[0]):
		o.dir = positional[0]
		return o, nil
	case sawOption:
		return nil, wrong
	}
	return nil, nil
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// uiDir is the folder to open: the one given, else $SEDIT_HOME, else the
// current one.
func uiDir(given string, getenv func(string) string) (string, error) {
	if given != "" {
		return given, nil
	}
	home := getenv("SEDIT_HOME")
	if home == "" {
		return ".", nil
	}
	if home == "~" || strings.HasPrefix(home, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, strings.TrimPrefix(home, "~"))
		}
	}
	if !isDir(home) {
		return "", fmt.Errorf("SEDIT_HOME=%s is not a folder", getenv("SEDIT_HOME"))
	}
	return home, nil
}

// Replaceable in tests.
var isTerminal = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func runUI(o *uiOptions) error {
	if !isTerminal() {
		return errors.New("the interface needs a terminal; in a script, give sedit a file or an option (see sedit --help)")
	}
	dir, err := uiDir(o.dir, os.Getenv)
	if err != nil {
		return err
	}
	app, err := tui.New(dir, tui.Options{Idle: o.idle, ClipboardTTL: o.clip})
	if err != nil {
		return err
	}
	return app.Run()
}
