package vault

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// BackupPath is where the previous version of path is kept.
func BackupPath(path string) string { return path + ".bak" }

// WriteAtomic writes data next to path and renames it into place, so a crash
// never leaves a half-written file. An existing file keeps its permissions; a
// new one is created with mode 0600.
func WriteAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".sedit-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name) // no-op after a successful rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	mode := fs.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Save encrypts content under password and writes it to path. prev is the
// file's existing ciphertext, which is kept as BackupPath(path) (still
// encrypted), or nil for a new file. The file keeps the title and description
// that prev has.
func Save(path string, password, content, prev []byte) error {
	var m Meta
	if prev != nil {
		m, _ = ReadMeta(prev)
	}
	return SaveMeta(path, password, content, prev, m)
}

// SaveMeta is Save with a title and description for the file, which replace any
// that prev has.
func SaveMeta(path string, password, content, prev []byte, m Meta) error {
	out, err := EncryptMeta(password, content, DefaultKDF, m)
	if err != nil {
		return err
	}
	if prev != nil {
		if err := WriteAtomic(BackupPath(path), prev); err != nil {
			return fmt.Errorf("writing backup: %w", err)
		}
	}
	return WriteAtomic(path, out)
}

// ResolvePath follows a symlink to the real file, so locking, backups and the
// atomic rename all act on the target instead of replacing the link. Paths
// that don't exist yet are returned unchanged.
func ResolvePath(path string) (string, error) {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("%s is a symlink whose target can't be resolved: %w", path, err)
	}
	return real, nil
}

// Changed reports whether the file at path is no longer what was read from it,
// prev, while it was being edited. A file that doesn't exist, or is empty, counts
// as having no content, which is what prev is for a new file.
func Changed(path string, prev []byte) (bool, error) {
	cur, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	return !bytes.Equal(cur, prev) && !(len(cur) == 0 && len(prev) == 0), nil
}

// ConflictError says that a file was changed by someone else while it was being
// edited, so the edit was not saved over it but beside it.
type ConflictError struct {
	Path     string // the file
	Conflict string // where the edited text was saved instead
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s was changed on disk while you were editing, so it was not overwritten; your text is saved as %s",
		filepath.Base(e.Path), filepath.Base(e.Conflict))
}

// SaveConflict writes content, encrypted under password, beside path as
// FILE.conflict (or .conflict.2, and so on, if that is taken) and returns the
// error that says so. It is for an edit that can't be saved over a file that was
// changed in the meantime.
func SaveConflict(path string, password, content []byte, m Meta) error {
	for n := 1; n <= 99; n++ {
		name := path + ".conflict"
		if n > 1 {
			name = fmt.Sprintf("%s.conflict.%d", path, n)
		}
		if _, err := os.Lstat(name); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := SaveMeta(name, password, content, nil, m); err != nil {
			return fmt.Errorf("%s changed on disk while you were editing, and your text could not be saved beside it: %w", filepath.Base(path), err)
		}
		return &ConflictError{Path: path, Conflict: name}
	}
	return fmt.Errorf("%s changed on disk while you were editing, and there are too many .conflict files beside it to save yours", filepath.Base(path))
}
