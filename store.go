package main

import (
	"errors"
	"path/filepath"
)

// PasswordStore remembers a file's passphrase outside the file, so the user
// doesn't have to retype it. Accounts are absolute file paths.
type PasswordStore interface {
	Get(account string) ([]byte, error) // ErrNotFound if there is no entry
	Set(account string, password []byte) error
	Delete(account string) error // ErrNotFound if there is no entry
}

var ErrNotFound = errors.New("no stored password")

// store is nil on platforms without a backend.
var store PasswordStore = newStore()

// account returns the store key for path: absolute, with symlinks resolved
// when the file exists.
func account(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real, nil
	}
	return abs, nil
}
