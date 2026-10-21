package vault

import (
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
// encrypted), or nil for a new file.
func Save(path string, password, content, prev []byte) error {
	out, err := Encrypt(password, content, DefaultKDF)
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
