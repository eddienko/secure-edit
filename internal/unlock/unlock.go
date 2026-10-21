// Package unlock opens a sedit file with whichever password is available: the
// file's own stored password, then the shared default, then one the caller asks
// the user for. It does no I/O with the user itself, so a command line and a
// full-screen interface can both use it.
package unlock

import (
	"errors"

	"github.com/eddienko/secure-edit/internal/keystore"
	"github.com/eddienko/secure-edit/internal/vault"
)

// Source says where the password used to open a file came from.
type Source int

const (
	SrcPrompt  Source = iota // typed by the user
	SrcFile                  // the file's own Keychain entry
	SrcDefault               // the shared default password
)

// Note describes the source for the user, or "" if it's unremarkable.
func (s Source) Note() string {
	switch s {
	case SrcFile:
		return "password from Keychain"
	case SrcDefault:
		return "default password from Keychain"
	}
	return ""
}

// Asker asks the user for a password, showing prompt.
type Asker func(prompt string) ([]byte, error)

// Stored decrypts data with a password that needs no typing: the file's own
// Keychain entry, then the default. A file entry that no longer works is removed.
// ok is false, with a nil error, if neither is available or fits: the caller
// should then ask the user. notify, which may be nil, reports things the user
// should know, such as a removed entry.
func Stored(path string, data []byte, notify func(string)) (password, plain []byte, src Source, ok bool, err error) {
	say := func(msg string) {
		if notify != nil {
			notify(msg)
		}
	}

	if keystore.Backend != nil {
		if acct, aerr := keystore.Account(path); aerr == nil {
			pw, gerr := keystore.Backend.Get(acct)
			switch {
			case gerr == nil:
				if plain, err = vault.Decrypt(pw, data); err == nil {
					return pw, plain, SrcFile, true, nil
				}
				vault.Wipe(pw)
				if !errors.Is(err, vault.ErrBadPassword) {
					return nil, nil, SrcPrompt, false, err
				}
				keystore.Backend.Delete(acct)
				say("stored password no longer works; removed it")
			case !errors.Is(gerr, keystore.ErrNotFound):
				say("can't read stored password: " + gerr.Error())
			}
		}
	}

	pw, perr := keystore.GetDefault()
	if perr != nil {
		if !errors.Is(perr, keystore.ErrNotFound) && !errors.Is(perr, keystore.ErrNoBackend) {
			say("can't read default password: " + perr.Error())
		}
		return nil, nil, SrcPrompt, false, nil
	}
	plain, err = vault.Decrypt(pw, data)
	if err == nil {
		return pw, plain, SrcDefault, true, nil
	}
	vault.Wipe(pw)
	if errors.Is(err, vault.ErrBadPassword) {
		// Normal for a file with a custom password. The default is never
		// removed: it belongs to many files, not just this one.
		return nil, nil, SrcPrompt, false, nil
	}
	return nil, nil, SrcPrompt, false, err
}

// Open decrypts data using Stored, and failing that asks the user. It returns
// the password, so that the file can be saved again, and the plaintext. The
// caller should vault.Wipe both when done.
func Open(path string, data []byte, ask Asker, notify func(string)) (password, plain []byte, src Source, err error) {
	if pw, pt, src, ok, err := Stored(path, data, notify); err != nil {
		return nil, nil, SrcPrompt, err
	} else if ok {
		return pw, pt, src, nil
	}
	if password, err = ask("Password: "); err != nil {
		return nil, nil, SrcPrompt, err
	}
	if plain, err = vault.Decrypt(password, data); err != nil {
		vault.Wipe(password)
		return nil, nil, SrcPrompt, err
	}
	return password, plain, SrcPrompt, nil
}

// StorePassword remembers password as the stored password of path.
func StorePassword(path string, password []byte) error {
	if keystore.Backend == nil {
		return keystore.ErrNoBackend
	}
	acct, err := keystore.Account(path)
	if err != nil {
		return err
	}
	return keystore.Backend.Set(acct, password)
}
