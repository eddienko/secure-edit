package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

const usage = `usage:
  sedit FILE           edit FILE (created if it doesn't exist)
  sedit -p FILE        decrypt FILE to stdout
  sedit --passwd FILE  change FILE's password

The editor is taken from $VISUAL, then $EDITOR, then vi.`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sedit:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	switch {
	case len(args) == 1 && !strings.HasPrefix(args[0], "-"):
		return edit(args[0])
	case len(args) == 2 && args[0] == "-p":
		return print(args[1])
	case len(args) == 2 && args[0] == "--passwd":
		return passwd(args[1])
	}
	return errors.New(usage)
}

func edit(path string) error {
	unlock, err := Lock(path)
	if err != nil {
		return err
	}
	defer unlock()

	data, err := os.ReadFile(path)
	var password, plain []byte
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if password, err = promptNew(); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if password, err = prompt("Password: "); err != nil {
			return err
		}
		// Fail before launching the editor if the password is wrong.
		if plain, err = Decrypt(password, data); err != nil {
			return err
		}
	}
	defer wipe(password)
	defer wipe(plain)

	edited, err := runEditor(plain)
	if err != nil {
		return err
	}
	defer wipe(edited)
	if data != nil && bytes.Equal(plain, edited) {
		fmt.Fprintln(os.Stderr, "no changes")
		return nil
	}
	out, err := Encrypt(password, edited, defaultKDF)
	if err != nil {
		return err
	}
	if data != nil {
		// data is the previous ciphertext, so the backup is encrypted too.
		if err := writeAtomic(backupPath(path), data); err != nil {
			return fmt.Errorf("writing backup: %w", err)
		}
	}
	return writeAtomic(path, out)
}

func backupPath(path string) string { return path + ".bak" }

func print(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	password, err := prompt("Password: ")
	if err != nil {
		return err
	}
	defer wipe(password)
	plain, err := Decrypt(password, data)
	if err != nil {
		return err
	}
	defer wipe(plain)
	_, err = os.Stdout.Write(plain)
	return err
}

func passwd(path string) error {
	unlock, err := Lock(path)
	if err != nil {
		return err
	}
	defer unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	old, err := prompt("Current password: ")
	if err != nil {
		return err
	}
	defer wipe(old)
	plain, err := Decrypt(old, data)
	if err != nil {
		return err
	}
	defer wipe(plain)
	password, err := promptNew()
	if err != nil {
		return err
	}
	defer wipe(password)
	out, err := Encrypt(password, plain, defaultKDF)
	if err != nil {
		return err
	}
	if _, err := os.Stat(backupPath(path)); err == nil {
		fmt.Fprintf(os.Stderr, "warning: %s is still encrypted with the old password\n", backupPath(path))
	}
	return writeAtomic(path, out)
}

// runEditor writes plain to a private temp file, runs the editor on it, and
// returns the resulting contents. The temp file is zeroed and removed after.
func runEditor(plain []byte) (edited []byte, err error) {
	dir, err := os.MkdirTemp("", "sedit-") // mode 0700
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	tmp := filepath.Join(dir, "secret.txt")
	defer shred(tmp)

	if err := os.WriteFile(tmp, plain, 0o600); err != nil {
		return nil, err
	}
	argv := editorCmd()
	argv = append(argv, tmp)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("editor failed, file not changed: %w", err)
	}
	return os.ReadFile(tmp)
}

// editorCmd returns the editor command line, with flags that stop vim-family
// editors from leaking plaintext into swap, backup, undo and viminfo files.
func editorCmd() []string {
	ed := os.Getenv("VISUAL")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	if ed == "" {
		ed = "vi"
	}
	argv := strings.Fields(ed)
	switch filepath.Base(argv[0]) {
	case "vi", "vim", "nvim", "view":
		argv = append(argv, "-n", "-i", "NONE",
			"-c", "set nobackup nowritebackup noundofile noswapfile viminfo=")
	}
	return argv
}

func shred(path string) {
	if st, err := os.Stat(path); err == nil {
		if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			f.Write(make([]byte, st.Size()))
			f.Sync()
			f.Close()
		}
	}
	os.Remove(path)
}

// writeAtomic writes data next to path and renames it into place.
func writeAtomic(path string, data []byte) error {
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

func prompt(msg string) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, msg)
		pw, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return pw, err
	}
	// Non-interactive: read one line from stdin (useful for scripting/tests).
	line, err := bufio.NewReader(os.Stdin).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	return bytes.TrimRight(line, "\r\n"), nil
}

func promptNew() ([]byte, error) {
	a, err := prompt("New password: ")
	if err != nil {
		return nil, err
	}
	if len(a) == 0 {
		return nil, errors.New("empty password not allowed")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return a, nil
	}
	b, err := prompt("Confirm password: ")
	if err != nil {
		return nil, err
	}
	defer wipe(b)
	if !bytes.Equal(a, b) {
		return nil, errors.New("passwords don't match")
	}
	return a, nil
}
