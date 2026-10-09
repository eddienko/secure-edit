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
  sedit [--default|--custom] FILE
                          edit FILE (created if it doesn't exist or is empty)
  sedit -p FILE           decrypt FILE to stdout
  sedit [--default|--custom] --passwd FILE
                          change FILE's password
  sedit [--default|--custom] --encrypt [-y] FILE
                          encrypt an existing plaintext FILE in place
  sedit --remember FILE   edit FILE and save its password in the macOS Keychain
  sedit --forget FILE     remove FILE's password from the Keychain
  sedit --set-default     store a default password in the macOS Keychain
  sedit --forget-default  remove the default password
  sedit -h, --help        show this help
  sedit --version         show the version

--default/--custom choose the password for a new file without asking.
A stored password (per file, then the default) is used instead of prompting.
A symlink is followed: the file it points to is edited, locked and backed up.
The editor is taken from $VISUAL, then $EDITOR, then vi.`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sedit:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	o, err := parseArgs(args)
	if err != nil {
		return err
	}
	switch o.mode {
	case "help":
		fmt.Println(usage)
		return nil
	case "version":
		fmt.Println("sedit", versionString())
		return nil
	}
	if o.file != "" {
		if o.file, err = resolvePath(o.file); err != nil {
			return err
		}
	}
	switch o.mode {
	case "edit":
		return edit(o.file, false, o.choice)
	case "remember":
		return edit(o.file, true, o.choice)
	case "print":
		return print(o.file)
	case "passwd":
		return passwd(o.file, o.choice)
	case "encrypt":
		return encrypt(o.file, o.yes, o.choice)
	case "forget":
		return forget(o.file)
	case "set-default":
		return setDefault()
	default: // forget-default
		return forgetDefault()
	}
}

func edit(path string, remember bool, choice pwChoice) (err error) {
	if remember && store == nil {
		return errNoStore
	}
	unlock, err := Lock(path)
	if err != nil {
		return err
	}
	defer unlock()

	data, err := os.ReadFile(path)
	var password, plain []byte
	switch {
	case errors.Is(err, fs.ErrNotExist), err == nil && len(data) == 0:
		// New or empty file: start fresh, and there's nothing to back up.
		data = nil
		if password, err = newPassword(choice); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if err := requireSedit(path, data); err != nil {
			return err
		}
		// Fail before launching the editor if the password is wrong.
		if password, plain, _, err = openExisting(path, data); err != nil {
			return err
		}
	}
	defer wipe(password)
	defer wipe(plain)
	defer func() {
		if err == nil && remember {
			err = storePassword(path, password)
		}
	}()

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

var errNoStore = errors.New("storing passwords isn't supported on this platform")

// openExisting decrypts data, using the file's stored password, then the default
// password, then prompting. A stored password that no longer works is removed.
func openExisting(path string, data []byte) (password, plain []byte, wasStored bool, err error) {
	if store != nil {
		if acct, aerr := account(path); aerr == nil {
			pw, gerr := store.Get(acct)
			switch {
			case gerr == nil:
				if plain, err = Decrypt(pw, data); err == nil {
					return pw, plain, true, nil
				}
				wipe(pw)
				if !errors.Is(err, ErrBadPassword) {
					return nil, nil, false, err
				}
				store.Delete(acct)
				fmt.Fprintln(os.Stderr, "sedit: stored password no longer works; removed it")
			case !errors.Is(gerr, ErrNotFound):
				fmt.Fprintln(os.Stderr, "sedit: can't read stored password:", gerr)
			}
		}
	}
	if pw, pt, ok, derr := tryDefault(data); derr != nil {
		return nil, nil, false, derr
	} else if ok {
		return pw, pt, false, nil
	}
	if password, err = prompt("Password: "); err != nil {
		return nil, nil, false, err
	}
	if plain, err = Decrypt(password, data); err != nil {
		wipe(password)
		return nil, nil, false, err
	}
	return password, plain, false, nil
}

func storePassword(path string, password []byte) error {
	acct, err := account(path)
	if err != nil {
		return err
	}
	return store.Set(acct, password)
}

func forget(path string) error {
	if store == nil {
		return errNoStore
	}
	acct, err := account(path)
	if err != nil {
		return err
	}
	if err := store.Delete(acct); errors.Is(err, ErrNotFound) {
		return fmt.Errorf("no stored password for %s", path)
	} else if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "removed stored password for", path)
	return nil
}

func backupPath(path string) string { return path + ".bak" }

// requireSedit fails early, before any password prompt, if data isn't a sedit file.
func requireSedit(path string, data []byte) error {
	if IsSedit(data) {
		return nil
	}
	return fmt.Errorf("%s is not a sedit file (use \"sedit --encrypt %s\" to convert a plaintext file)", path, path)
}

// encrypt converts an existing plaintext file to a sedit file in place.
func encrypt(path string, yes bool, choice pwChoice) error {
	unlock, err := Lock(path)
	if err != nil {
		return err
	}
	defer unlock()

	plain, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	defer wipe(plain)
	if IsSedit(plain) {
		return fmt.Errorf("%s is already a sedit file", path)
	}
	if len(plain) == 0 {
		return fmt.Errorf("%s is empty; just run \"sedit %s\"", path, path)
	}
	if !yes {
		if err := confirm(fmt.Sprintf("Encrypt %s in place? The old plaintext can't be reliably erased from disk or backups. [y/N] ", path)); err != nil {
			return err
		}
	}
	password, err := newPassword(choice)
	if err != nil {
		return err
	}
	defer wipe(password)
	out, err := Encrypt(password, plain, defaultKDF)
	if err != nil {
		return err
	}
	// No .bak here: it would be a copy of the plaintext.
	return writeAtomic(path, out)
}

func confirm(msg string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("not a terminal; pass -y to skip confirmation")
	}
	fmt.Fprint(os.Stderr, msg)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		return errors.New("aborted")
	}
	return nil
}

func print(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := requireSedit(path, data); err != nil {
		return err
	}
	password, plain, _, err := openExisting(path, data)
	if err != nil {
		return err
	}
	defer wipe(password)
	defer wipe(plain)
	_, err = os.Stdout.Write(plain)
	return err
}

func passwd(path string, choice pwChoice) error {
	unlock, err := Lock(path)
	if err != nil {
		return err
	}
	defer unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := requireSedit(path, data); err != nil {
		return err
	}
	old, plain, wasStored, err := openExisting(path, data)
	if err != nil {
		return err
	}
	defer wipe(old)
	defer wipe(plain)
	password, err := newPassword(choice)
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
	if err := writeAtomic(path, out); err != nil {
		return err
	}
	if wasStored {
		if err := storePassword(path, password); err != nil {
			return fmt.Errorf("password changed, but updating the stored one failed (run \"sedit --forget %s\"): %w", path, err)
		}
	}
	return nil
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
