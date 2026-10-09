package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// defaultAccount is the store key for the shared default password. It can't
// collide with a file entry, because those are absolute paths.
const defaultAccount = "<default>"

// pwChoice says which password a new file (or a re-keyed one) should get.
type pwChoice int

const (
	choiceAsk     pwChoice = iota // ask if a default exists and we're on a terminal
	choiceDefault                 // the stored default
	choiceCustom                  // a new password typed by the user
)

var errNoDefault = errors.New(`no default password set (run "sedit --set-default")`)

// getDefault returns the stored default, or ErrNotFound / errNoStore.
func getDefault() ([]byte, error) {
	if store == nil {
		return nil, errNoStore
	}
	return store.Get(defaultAccount)
}

// newPassword picks the password for a new or re-keyed file.
func newPassword(choice pwChoice) ([]byte, error) {
	switch choice {
	case choiceCustom:
		return promptNew()
	case choiceDefault:
		pw, err := getDefault()
		if errors.Is(err, ErrNotFound) {
			return nil, errNoDefault
		}
		return pw, err
	}
	// Ask only if there is a default to choose, and a terminal to ask on.
	// Otherwise (including scripts) use a custom password, so the default is
	// never picked up by accident.
	pw, err := getDefault()
	wipe(pw)
	if err != nil || !term.IsTerminal(int(os.Stdin.Fd())) {
		return promptNew()
	}
	in := bufio.NewReader(os.Stdin)
	for {
		fmt.Fprint(os.Stderr, "Password: [d]efault or [c]ustom? ")
		line, err := in.ReadString('\n')
		if c, ok := parseChoice(line); ok {
			if c == choiceDefault {
				return getDefault()
			}
			return promptNew()
		}
		if err != nil {
			return nil, errors.New("no choice made")
		}
	}
}

// parseChoice understands the answer to the default/custom question.
func parseChoice(line string) (pwChoice, bool) {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "d", "default":
		return choiceDefault, true
	case "c", "custom":
		return choiceCustom, true
	}
	return choiceAsk, false
}

// tryDefault opens data with the stored default. It reports ok=false, without
// error, if there is no default or it doesn't fit this file. The default is
// never removed: it belongs to many files, not just this one.
func tryDefault(data []byte) (password, plain []byte, ok bool, err error) {
	pw, err := getDefault()
	if err != nil {
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, errNoStore) {
			fmt.Fprintln(os.Stderr, "sedit: can't read default password:", err)
		}
		return nil, nil, false, nil
	}
	plain, err = Decrypt(pw, data)
	if err == nil {
		return pw, plain, true, nil
	}
	wipe(pw)
	if errors.Is(err, ErrBadPassword) {
		return nil, nil, false, nil // normal for files with a custom password
	}
	return nil, nil, false, err
}

func setDefault() error {
	if store == nil {
		return errNoStore
	}
	old, getErr := getDefault()
	wipe(old)
	pw, err := promptNew()
	if err != nil {
		return err
	}
	defer wipe(pw)
	if err := store.Set(defaultAccount, pw); err != nil {
		return err
	}
	if getErr == nil {
		fmt.Fprintln(os.Stderr, "note: existing files are not re-keyed; files that used the previous default still need that password")
	}
	return nil
}

func forgetDefault() error {
	if store == nil {
		return errNoStore
	}
	if err := store.Delete(defaultAccount); errors.Is(err, ErrNotFound) {
		return errNoDefault
	} else if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "removed default password")
	return nil
}
