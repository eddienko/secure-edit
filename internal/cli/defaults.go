package cli

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/eddienko/secure-edit/internal/keystore"
	"github.com/eddienko/secure-edit/internal/vault"
	"os"
	"strings"
)

// pwChoice says which password a new file (or a re-keyed one) should get.
type pwChoice int

const (
	choiceAsk     pwChoice = iota // ask if a default exists and we're on a terminal
	choiceDefault                 // the stored default
	choiceCustom                  // a new password typed by the user
)

var errNoDefault = errors.New(`no default password set (run "sedit --set-default")`)

// newPassword picks the password for a new or re-keyed file.
func newPassword(choice pwChoice) ([]byte, error) {
	switch choice {
	case choiceCustom:
		return promptNew()
	case choiceDefault:
		pw, err := keystore.GetDefault()
		if errors.Is(err, keystore.ErrNotFound) {
			return nil, errNoDefault
		}
		return pw, err
	}
	// Ask only if there is a default to choose, and a terminal to ask on.
	// Otherwise (including scripts) use a custom password, so the default is
	// never picked up by accident.
	pw, err := keystore.GetDefault()
	vault.Wipe(pw)
	tty, ok := promptTerminal()
	if err != nil || !ok {
		return promptNew()
	}
	in := bufio.NewReader(tty)
	for {
		fmt.Fprint(os.Stderr, "Password: [d]efault or [c]ustom? ")
		line, err := in.ReadString('\n')
		if c, ok := parseChoice(line); ok {
			if c == choiceDefault {
				return keystore.GetDefault()
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

func setDefault() error {
	if keystore.Backend == nil {
		return keystore.ErrNoBackend
	}
	old, getErr := keystore.GetDefault()
	vault.Wipe(old)
	pw, err := promptNew()
	if err != nil {
		return err
	}
	defer vault.Wipe(pw)
	if err := keystore.Backend.Set(keystore.DefaultAccount, pw); err != nil {
		return err
	}
	if getErr == nil {
		fmt.Fprintln(os.Stderr, "note: existing files are not re-keyed; files that used the previous default still need that password")
	}
	return nil
}

func forgetDefault() error {
	if keystore.Backend == nil {
		return keystore.ErrNoBackend
	}
	if err := keystore.Backend.Delete(keystore.DefaultAccount); errors.Is(err, keystore.ErrNotFound) {
		return errNoDefault
	} else if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "removed default password")
	return nil
}
