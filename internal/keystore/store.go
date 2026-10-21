// Package keystore remembers passwords outside the files they protect: on
// macOS in the login keychain, elsewhere not at all. Entries are keyed by an
// account string, which for a file is its absolute path.
package keystore

import (
	"errors"
	"path/filepath"
)

// Store keeps passwords outside the files they protect, so the user doesn't have
// to retype them. Accounts are absolute file paths, or DefaultAccount.
type Store interface {
	Get(account string) ([]byte, error) // ErrNotFound if there is no entry
	Set(account string, password []byte) error
	Delete(account string) error // ErrNotFound if there is no entry
}

// ErrNotFound is returned by a Store when there is no entry for an account.
var ErrNotFound = errors.New("no stored password")

// Backend is where passwords are kept. It is nil on platforms without one, and
// is replaced by a fake in tests.
var Backend Store = newStore()

// ErrNoBackend is returned by operations that need a Backend when there is none.
var ErrNoBackend = errors.New("storing passwords isn't supported on this platform")

// DefaultAccount is the key of the shared default password. It can't collide
// with a file entry, because those are absolute paths.
const DefaultAccount = "<default>"

// GetDefault returns the stored default password, ErrNotFound if there is none,
// or ErrNoBackend.
func GetDefault() ([]byte, error) {
	if Backend == nil {
		return nil, ErrNoBackend
	}
	return Backend.Get(DefaultAccount)
}

// Account returns the store key for path: absolute, with symlinks resolved
// when the file exists.
func Account(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real, nil
	}
	return abs, nil
}
