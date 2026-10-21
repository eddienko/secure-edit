package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/eddienko/secure-edit/internal/editor"
	"github.com/eddienko/secure-edit/internal/fileops"
	"github.com/eddienko/secure-edit/internal/unlock"
	"github.com/eddienko/secure-edit/internal/vault"
)

// Session is one sedit file held open in memory: the encrypted bytes as read,
// the password that opens them, and the decrypted text. Wipe zeroes the secrets.
type Session struct {
	Path     string
	Cipher   []byte // the file as last read; nil for a file that doesn't exist yet
	Password []byte
	Plain    []byte
	Src      unlock.Source

	// NewMeta is the title and description a file that doesn't exist yet is
	// created with. (An existing file's are in its header.)
	NewMeta vault.Meta
}

// OpenSession reads the sedit file at path, still locked.
func OpenSession(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !vault.IsSedit(data) {
		return nil, fmt.Errorf("%s is not a sedit file", path)
	}
	return &Session{Path: path, Cipher: data}, nil
}

// NewSession starts a file that doesn't exist yet, already unlocked with an
// empty text, to be filled in with Edit.
func NewSession(path string, password []byte) *Session {
	return &Session{Path: path, Password: password, Plain: []byte{}}
}

// Meta is the file's title and description.
func (s *Session) Meta() vault.Meta {
	if s.Cipher == nil {
		return s.NewMeta
	}
	m, _ := vault.ReadMeta(s.Cipher)
	return m
}

// Locked reports whether the text is not available.
func (s *Session) Locked() bool { return s.Plain == nil }

// TryStored unlocks with a password that needs no typing: the file's own stored
// one, then the default. ok is false if none is available or fits, in which
// case the user has to type one.
func (s *Session) TryStored(notify func(string)) (ok bool, err error) {
	pw, plain, src, ok, err := unlock.Stored(s.Path, s.Cipher, notify)
	if err != nil || !ok {
		return false, err
	}
	s.Password, s.Plain, s.Src = pw, plain, src
	return true, nil
}

// UnlockWith unlocks with a password the user typed. On success the session
// owns the password slice; on failure it is left for the caller to wipe.
func (s *Session) UnlockWith(password []byte) error {
	plain, err := vault.Decrypt(password, s.Cipher)
	if err != nil {
		return err
	}
	s.Password, s.Plain, s.Src = password, plain, unlock.SrcPrompt
	return nil
}

// Wipe zeroes the password and the text and locks the session.
func (s *Session) Wipe() {
	vault.Wipe(s.Password)
	vault.Wipe(s.Plain)
	s.Password, s.Plain = nil, nil
}

// refresh makes sure the session matches what is on disk, so that a change made
// by someone else is not overwritten by a stale copy.
func (s *Session) refresh() error {
	data, err := os.ReadFile(s.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if s.Cipher != nil {
			return fmt.Errorf("%s has disappeared", s.Path)
		}
		return nil // a new file, still to be created
	case err != nil:
		return err
	case s.Cipher == nil:
		return fmt.Errorf("%s has appeared since you started; refusing to overwrite it", s.Path)
	case bytes.Equal(data, s.Cipher):
		return nil
	}
	plain, err := vault.Decrypt(s.Password, data)
	if err != nil {
		return fmt.Errorf("%s was changed by someone else and this password no longer opens it; lock it and unlock again", s.Path)
	}
	vault.Wipe(s.Plain)
	s.Plain, s.Cipher = plain, data
	return nil
}

// Edit opens the text in the user's editor and saves what comes back, keeping
// the previous version as FILE.bak. It holds the file's lock while the editor
// runs. It reports whether anything was saved: a new file that was left empty
// is not created. The editor takes over the terminal, so call Edit from inside
// Application.Suspend.
func (s *Session) Edit() (saved bool, err error) {
	if s.Locked() {
		return false, errors.New("the file is locked")
	}
	release, err := vault.Lock(s.Path)
	if err != nil {
		return false, err
	}
	defer func() {
		release()
		if s.Cipher == nil && !saved {
			os.Remove(vault.LockPath(s.Path)) // we only locked a file that was never created
		}
	}()
	if err := s.refresh(); err != nil {
		return false, err
	}
	edited, err := editor.Run(s.Path, s.Plain)
	if err != nil {
		return false, err
	}
	if s.Cipher == nil && len(edited) == 0 {
		return false, nil
	}
	if s.Cipher != nil && bytes.Equal(edited, s.Plain) {
		vault.Wipe(edited)
		return false, nil
	}
	var saveErr error
	if s.Cipher == nil {
		saveErr = vault.SaveMeta(s.Path, s.Password, edited, nil, s.NewMeta)
	} else {
		saveErr = vault.Save(s.Path, s.Password, edited, s.Cipher)
	}
	if saveErr != nil {
		vault.Wipe(edited)
		return false, saveErr
	}
	vault.Wipe(s.Plain)
	s.Plain = edited
	s.Cipher, err = os.ReadFile(s.Path)
	return true, err
}

// SaveText saves edited, which the session takes ownership of, as the new text
// of a file that exists, keeping the previous version as FILE.bak. It refuses if
// the file has changed on disk since the session read it, so that what someone
// else wrote is not overwritten. It reports whether anything was saved.
func (s *Session) SaveText(edited []byte) (saved bool, err error) {
	if s.Locked() {
		return false, errors.New("the file is locked")
	}
	if s.Cipher == nil {
		return false, errors.New("the file does not exist yet")
	}
	release, err := vault.Lock(s.Path)
	if err != nil {
		return false, err
	}
	defer release()
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(data, s.Cipher) {
		return false, fmt.Errorf("%s was changed on disk since you opened it; close without saving and open it again", s.Path)
	}
	if bytes.Equal(edited, s.Plain) {
		vault.Wipe(edited)
		return false, nil
	}
	if err := vault.Save(s.Path, s.Password, edited, s.Cipher); err != nil {
		return false, err
	}
	vault.Wipe(s.Plain)
	s.Plain = edited
	s.Cipher, err = os.ReadFile(s.Path)
	return true, err
}

// Rekey changes the file's password. The session then holds the new one, and
// takes ownership of newPassword.
func (s *Session) Rekey(newPassword []byte) (fileops.Result, error) {
	if s.Locked() {
		return fileops.Result{}, errors.New("the file is locked")
	}
	release, err := vault.Lock(s.Path)
	if err != nil {
		return fileops.Result{}, err
	}
	defer release()
	if err := s.refresh(); err != nil {
		return fileops.Result{}, err
	}
	res, err := fileops.Rekey(s.Path, s.Plain, newPassword, s.Src == unlock.SrcFile)
	var storeErr *fileops.StoreUpdateError
	if err != nil && !errors.As(err, &storeErr) {
		return res, err // the file was not changed
	}
	vault.Wipe(s.Password)
	s.Password = newPassword
	if s.Src != unlock.SrcFile {
		s.Src = unlock.SrcPrompt
	}
	if data, rerr := os.ReadFile(s.Path); rerr == nil {
		s.Cipher = data
	}
	return res, err // a StoreUpdateError is still reported, but the password did change
}

// SetMeta changes the file's title and description, leaving the text alone.
func (s *Session) SetMeta(m vault.Meta) error {
	if s.Locked() {
		return errors.New("the file is locked")
	}
	if s.Cipher == nil {
		s.NewMeta = m // not created yet: it will be with the first save
		return m.Validate()
	}
	release, err := vault.Lock(s.Path)
	if err != nil {
		return err
	}
	defer release()
	if err := s.refresh(); err != nil {
		return err
	}
	if err := fileops.SetMeta(s.Path, s.Plain, s.Password, m); err != nil {
		return err
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return err
	}
	s.Cipher = data
	return nil
}
