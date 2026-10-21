// Package fileops moves and removes sedit files together with everything that
// belongs to them: the FILE.bak backup, the password remembered for the file in
// the Keychain, and its lock file. A plain mv or rm would leave the backup
// behind and orphan the Keychain entry.
package fileops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/eddienko/secure-edit/internal/keystore"
	"github.com/eddienko/secure-edit/internal/vault"
)

// Result describes what was done besides the file itself.
type Result struct {
	Backup   bool     // FILE.bak was moved or removed too
	Entry    bool     // the remembered password was moved or removed too
	Warnings []string // things that didn't work but didn't stop the operation
}

func (r *Result) warn(format string, a ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, a...))
}

// rename is os.Rename, replaceable in tests.
var rename = os.Rename

// check makes sure path is a plain sedit file: not missing, a directory or a
// symlink (which would be left dangling), and with the sedit magic bytes.
func check(path string) (os.FileInfo, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return nil, fmt.Errorf("%s is a symlink; use the file it points to", path)
	case fi.IsDir():
		return nil, fmt.Errorf("%s is a directory", path)
	case !fi.Mode().IsRegular():
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, 64)
	n, _ := f.Read(head)
	if !vault.IsSedit(head[:n]) {
		return nil, fmt.Errorf("%s is not a sedit file", path)
	}
	return fi, nil
}

// Check reports why path can't be moved or removed as a sedit file, or nil if it
// can. Callers use it to vet every file before changing any of them.
func Check(path string) error {
	_, err := check(path)
	return err
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// storedPassword returns the password remembered for path (nil if none) and
// the account it is filed under. Call it before the file moves or disappears:
// the account is the file's resolved path.
func storedPassword(path string, res *Result) (pw []byte, account string) {
	if keystore.Backend == nil {
		return nil, ""
	}
	account, err := keystore.Account(path)
	if err != nil {
		return nil, ""
	}
	pw, err = keystore.Backend.Get(account)
	switch {
	case err == nil:
		return pw, account
	case !errors.Is(err, keystore.ErrNotFound):
		res.warn("can't read the stored password: %v", err)
	}
	return nil, account
}

// Move renames the sedit file oldPath to newPath, and moves its backup and its
// remembered password with it. If newPath is an existing directory the file
// goes inside it under its own name. It never overwrites anything. The final
// location is returned.
func Move(oldPath, newPath string) (dest string, res Result, err error) {
	fi, err := check(oldPath)
	if err != nil {
		return "", res, err
	}

	dest = newPath
	if d, serr := os.Stat(newPath); serr == nil && d.IsDir() {
		dest = filepath.Join(newPath, filepath.Base(oldPath))
	}
	if dir := filepath.Dir(dest); !isDir(dir) {
		return "", res, fmt.Errorf("%s: no such directory", dir)
	}
	if exists(dest) {
		// A case-only rename on a case-insensitive file system names the same
		// file; anything else would overwrite.
		if d, derr := os.Lstat(dest); !(derr == nil && os.SameFile(fi, d) && strings.EqualFold(oldPath, dest) && oldPath != dest) {
			return "", res, fmt.Errorf("refusing to overwrite %s", dest)
		}
	}
	oldBak, newBak := vault.BackupPath(oldPath), vault.BackupPath(dest)
	hasBak := exists(oldBak)
	if hasBak && exists(newBak) && !strings.EqualFold(oldBak, newBak) {
		return "", res, fmt.Errorf("refusing to overwrite the backup %s", newBak)
	}

	release, err := vault.Lock(oldPath)
	if err != nil {
		return "", res, err
	}
	defer func() {
		release()
		os.Remove(vault.LockPath(oldPath)) // best effort: nothing is at that name now
	}()

	pw, oldAccount := storedPassword(oldPath, &res)
	defer vault.Wipe(pw)

	if err := move(oldPath, dest, fi); err != nil {
		return "", res, err
	}
	if hasBak {
		if bi, berr := os.Lstat(oldBak); berr == nil {
			if err := move(oldBak, newBak, bi); err != nil {
				res.warn("the file moved, but its backup did not: %v", err)
			} else {
				res.Backup = true
			}
		}
	}

	if pw != nil {
		newAccount, err := keystore.Account(dest)
		if err == nil {
			err = keystore.Backend.Set(newAccount, pw)
		}
		if err != nil {
			res.warn("couldn't move the stored password (the old entry was kept): %v", err)
		} else {
			if derr := keystore.Backend.Delete(oldAccount); derr != nil && !errors.Is(derr, keystore.ErrNotFound) {
				res.warn("couldn't remove the old stored password: %v", derr)
			}
			res.Entry = true
		}
	}
	return dest, res, nil
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// move renames, falling back to copy and delete across file systems. The file
// keeps its permissions.
func move(from, to string, fi os.FileInfo) error {
	err := rename(from, to)
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	defer vault.Wipe(data)
	if err := vault.WriteAtomic(to, data); err != nil {
		return err
	}
	if err := os.Chmod(to, fi.Mode().Perm()); err != nil {
		return err
	}
	return os.Remove(from)
}

// Remove deletes the sedit file at path, its backup, its remembered password
// and its lock file. This isn't a secure erase: the encrypted data may remain
// in backups and in freed disk blocks.
func Remove(path string) (res Result, err error) {
	if _, err := check(path); err != nil {
		return res, err
	}
	release, err := vault.Lock(path)
	if err != nil {
		return res, err
	}
	defer func() {
		release()
		os.Remove(vault.LockPath(path))
	}()

	pw, account := storedPassword(path, &res)
	vault.Wipe(pw) // only the account matters here
	hadEntry := pw != nil

	if err := os.Remove(path); err != nil {
		return res, err
	}
	switch err := os.Remove(vault.BackupPath(path)); {
	case err == nil:
		res.Backup = true
	case !os.IsNotExist(err):
		res.warn("couldn't remove the backup: %v", err)
	}
	if account != "" && hadEntry {
		if derr := keystore.Backend.Delete(account); derr != nil && !errors.Is(derr, keystore.ErrNotFound) {
			res.warn("couldn't remove the stored password: %v", derr)
		} else {
			res.Entry = true
		}
	}
	return res, nil
}

// metaOf is the title and description in the header of the file at path, or none
// if it can't be read.
func metaOf(path string) vault.Meta {
	data, err := os.ReadFile(path)
	if err != nil {
		return vault.Meta{}
	}
	m, _ := vault.ReadMeta(data)
	return m
}

// SetMeta replaces the title and description of the sedit file at path, which
// the caller has opened with password (so has plain) and should hold
// vault.Lock(path) for. The text is not touched. Like Rekey it makes no backup:
// a copy would only repeat the old labels.
func SetMeta(path string, plain, password []byte, m vault.Meta) error {
	out, err := vault.EncryptMeta(password, plain, vault.DefaultKDF, m)
	if err != nil {
		return err
	}
	return vault.WriteAtomic(path, out)
}

// StoreUpdateError means a file was re-encrypted under a new password but the
// password remembered for it could not be updated. The file is fine, and the
// stale entry is harmless but no longer works (it is removed on next use).
type StoreUpdateError struct {
	Path string
	Err  error
}

func (e *StoreUpdateError) Error() string {
	return fmt.Sprintf("password changed, but updating the stored one failed (run \"sedit --forget %s\"): %v", e.Path, e.Err)
}

func (e *StoreUpdateError) Unwrap() error { return e.Err }

// Rekey re-encrypts plain under newPassword and writes it to path, replacing the
// file. No backup is made, because it would be a copy under the old password; an
// existing FILE.bak still is one, which is reported as a warning. If
// updateStored is set, the password remembered for the file is replaced too.
//
// The caller opens the file (so has plain) and should hold vault.Lock(path).
func Rekey(path string, plain, newPassword []byte, updateStored bool) (res Result, err error) {
	out, err := vault.EncryptMeta(newPassword, plain, vault.DefaultKDF, metaOf(path))
	if err != nil {
		return res, err
	}
	if exists(vault.BackupPath(path)) {
		res.warn("%s is still encrypted with the old password", vault.BackupPath(path))
	}
	if err := vault.WriteAtomic(path, out); err != nil {
		return res, err
	}
	if updateStored && keystore.Backend != nil {
		account, err := keystore.Account(path)
		if err == nil {
			err = keystore.Backend.Set(account, newPassword)
		}
		if err != nil {
			return res, &StoreUpdateError{Path: path, Err: err}
		}
		res.Entry = true
	}
	return res, nil
}
