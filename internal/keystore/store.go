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

// Haser is implemented by a Store that can say whether an entry exists without
// reading the password. On macOS that needs no approval from the Keychain, which
// reading a password can.
type Haser interface {
	Has(account string) (bool, error)
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

// Has reports whether there is an entry for account. A store that can't answer
// without reading the password is asked to read it, and the copy is wiped at
// once. It is ErrNoBackend if there is no store.
func Has(account string) (bool, error) {
	if Backend == nil {
		return false, ErrNoBackend
	}
	if h, ok := Backend.(Haser); ok {
		return h.Has(account)
	}
	pw, err := Backend.Get(account)
	for i := range pw {
		pw[i] = 0
	}
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotFound):
		return false, nil
	}
	return false, err
}

// HasDefault reports whether a default password is stored.
func HasDefault() (bool, error) { return Has(DefaultAccount) }

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
