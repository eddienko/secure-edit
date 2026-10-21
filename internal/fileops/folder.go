package fileops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/eddienko/secure-edit/internal/keystore"
	"github.com/eddienko/secure-edit/internal/vault"
)

// ValidName checks a name for a new file or folder: one path element that the
// list will show (it hides names starting with a dot, and backups).
func ValidName(name string) error {
	switch {
	case name == "":
		return errors.New("give it a name")
	case name != filepath.Base(name) || name == "." || name == "..":
		return errors.New("give a name, not a path (move it afterwards with m)")
	case strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".bak"):
		return errors.New("names starting with . or ending in .bak are hidden from the list")
	}
	return nil
}

// MkDir makes the folder parent/name, private to you (mode 0700), with a title
// and description if m has any. It returns the new path. It never uses an
// existing name.
func MkDir(parent, name string, m vault.Meta) (string, error) {
	if err := ValidName(name); err != nil {
		return "", err
	}
	if err := m.Clean().Validate(); err != nil {
		return "", err
	}
	if !isDir(parent) {
		return "", fmt.Errorf("%s: no such folder", parent)
	}
	path := filepath.Join(parent, name)
	if exists(path) {
		return "", fmt.Errorf("%s already exists", name)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		return "", err
	}
	if err := vault.WriteFolderMeta(path, m); err != nil {
		os.Remove(path) // nothing else is in it yet
		return "", err
	}
	return path, nil
}

// checkDir makes sure path is a real folder: not missing, a file or a symlink
// (which would be left dangling, or have its target moved instead).
func checkDir(path string) (os.FileInfo, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return nil, fmt.Errorf("%s is a symlink; use the folder it points to", path)
	case !fi.IsDir():
		return nil, fmt.Errorf("%s is not a folder", path)
	}
	return fi, nil
}

// seditFilesIn lists the sedit files under dir (not their .bak backups), not
// following symlinks.
func seditFilesIn(dir string) []string {
	var out []string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() || strings.HasSuffix(path, ".bak") {
			return nil
		}
		if f, err := os.Open(path); err == nil {
			head := make([]byte, 64)
			n, _ := f.Read(head)
			f.Close()
			if vault.IsSedit(head[:n]) {
				out = append(out, path)
			}
		}
		return nil
	})
	return out
}

// MoveDir renames the folder oldPath to exactly newPath, which must not exist,
// and re-files the password remembered for each sedit file inside it, because
// those are kept under the file's path. Backups, lock files and the folder's own
// title and description move with it. It refuses a symlink, a move into itself
// and a move to another file system, and if any file in it is being edited it
// changes nothing. A password that can't be moved is a warning, not a failure:
// the folder has moved by then.
func MoveDir(oldPath, newPath string) (res Result, err error) {
	fi, err := checkDir(oldPath)
	if err != nil {
		return res, err
	}
	if !isDir(filepath.Dir(newPath)) {
		return res, fmt.Errorf("%s: no such folder", filepath.Dir(newPath))
	}
	if inside(oldPath, newPath) {
		return res, errors.New("a folder can't be moved into itself")
	}
	if exists(newPath) {
		// A case-only rename on a case-insensitive file system names the same
		// folder; anything else would overwrite.
		if d, derr := os.Lstat(newPath); !(derr == nil && os.SameFile(fi, d) && strings.EqualFold(oldPath, newPath) && oldPath != newPath) {
			return res, fmt.Errorf("refusing to overwrite %s", newPath)
		}
	}

	files := seditFilesIn(oldPath)
	var releases []func()
	defer func() {
		for _, r := range releases {
			r()
		}
	}()
	for _, f := range files {
		release, lerr := vault.Lock(f)
		if lerr != nil {
			return res, lerr // nothing has changed
		}
		releases = append(releases, release)
	}

	type kept struct {
		rel, account string
		pw           []byte
	}
	var passwords []kept
	for _, f := range files {
		if pw, account := storedPassword(f, &res); pw != nil {
			rel, _ := filepath.Rel(oldPath, f)
			passwords = append(passwords, kept{rel, account, pw})
		}
	}
	defer func() {
		for _, k := range passwords {
			vault.Wipe(k.pw)
		}
	}()

	if rerr := rename(oldPath, newPath); rerr != nil {
		if errors.Is(rerr, syscall.EXDEV) {
			return res, errors.New("can't move a folder to another file system; move its files one by one")
		}
		return res, rerr
	}
	res.Files = len(files)
	for _, k := range passwords {
		account, aerr := keystore.Account(filepath.Join(newPath, k.rel))
		if aerr == nil {
			aerr = keystore.Backend.Set(account, k.pw)
		}
		if aerr != nil {
			res.warn("couldn't move the stored password of %s (the old entry was kept): %v", k.rel, aerr)
			continue
		}
		if derr := keystore.Backend.Delete(k.account); derr != nil && !errors.Is(derr, keystore.ErrNotFound) {
			res.warn("couldn't remove the old stored password of %s: %v", k.rel, derr)
		}
		res.Passwords++
	}
	return res, nil
}

// inside reports whether path is dir itself or somewhere under it, with
// symlinks resolved as far as they can be.
func inside(dir, path string) bool {
	resolve := func(p string) string {
		abs, err := filepath.Abs(p)
		if err != nil {
			return p
		}
		if real, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
			return filepath.Join(real, filepath.Base(abs))
		}
		return abs
	}
	d, p := resolve(dir), resolve(path)
	rel, err := filepath.Rel(d, p)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

// NotEmptyError means a folder holds something besides what goes with it.
type NotEmptyError struct {
	Path  string
	Names []string // what it holds; folders end in /
}

func (e *NotEmptyError) Error() string {
	shown := e.Names
	more := ""
	if len(shown) > 4 {
		shown, more = shown[:4], fmt.Sprintf(" and %d more", len(e.Names)-4)
	}
	return fmt.Sprintf("%s is not empty: it holds %s%s", filepath.Base(e.Path), strings.Join(shown, ", "), more)
}

// bookkeeping reports whether a folder entry is just something sedit or macOS
// leaves behind: the folder's title file, a Finder .DS_Store, or an empty lock
// file.
func bookkeeping(dir string, e os.DirEntry) bool {
	name := e.Name()
	if !e.Type().IsRegular() {
		return false
	}
	switch {
	case name == vault.FolderMetaName, name == ".DS_Store":
		return true
	case strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".lock"):
		fi, err := e.Info()
		return err == nil && fi.Size() == 0
	}
	return false
}

// emptyDirLeftovers checks that the folder at path is empty, and returns the
// bookkeeping files in it, which go with it. It is a *NotEmptyError if the
// folder holds anything else.
func emptyDirLeftovers(path string) ([]string, error) {
	if _, err := checkDir(path); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var held, leftovers []string
	for _, e := range entries {
		if bookkeeping(path, e) {
			leftovers = append(leftovers, e.Name())
			continue
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		held = append(held, name)
	}
	if len(held) > 0 {
		return nil, &NotEmptyError{Path: path, Names: held}
	}
	return leftovers, nil
}

// CheckEmptyDir reports why the folder at path can't be deleted, or nil if it
// can: it is empty, which includes holding nothing but its title file,
// .DS_Store files and empty lock files.
func CheckEmptyDir(path string) error {
	_, err := emptyDirLeftovers(path)
	return err
}

// RemoveEmptyDir deletes the folder at path if CheckEmptyDir allows it, together
// with the bookkeeping files in it. Otherwise it changes nothing.
func RemoveEmptyDir(path string) error {
	leftovers, err := emptyDirLeftovers(path)
	if err != nil {
		return err
	}
	for _, name := range leftovers {
		if err := os.Remove(filepath.Join(path, name)); err != nil {
			return err
		}
	}
	return os.Remove(path)
}
