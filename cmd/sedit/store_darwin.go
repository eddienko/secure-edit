//go:build darwin

package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const keychainService = "sedit"

// keychainStore keeps passphrases in the login keychain using /usr/bin/security.
//
// Any process running as the user can read these items without a prompt while
// the keychain is unlocked (the security tool is trusted on items it creates).
// Passphrases are stored base64-encoded so arbitrary bytes round-trip, and are
// sent to security over stdin so they never appear in the process list.
type keychainStore struct{}

func newStore() PasswordStore { return keychainStore{} }

func (keychainStore) Get(account string) ([]byte, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-s", keychainService, "-a", account, "-w").Output()
	if err != nil {
		if isExit(err, 44) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("keychain: %w", err)
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
}

func (keychainStore) Set(account string, password []byte) error {
	cmd := exec.Command("security", "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf(
		"add-generic-password -U -s %s -a %s -w %s\n",
		keychainService, shellQuote(account), base64.StdEncoding.EncodeToString(password)))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("keychain: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func (keychainStore) Delete(account string) error {
	err := exec.Command("security", "delete-generic-password",
		"-s", keychainService, "-a", account).Run()
	if isExit(err, 44) {
		return ErrNotFound
	}
	return err
}

func isExit(err error, code int) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == code
}

// shellQuote quotes s for security's interactive command parser.
func shellQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
