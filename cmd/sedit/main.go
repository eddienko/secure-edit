package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
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
  sedit --share FILE (--to KEY | -R FILE)... [-o OUT] [-a]
                          write a copy of FILE for others to decrypt with "age"
  sedit [--default|--custom] --import FILE.age [-i KEY]... [-o OUT]
                          turn an age-encrypted FILE.age into a sedit file
  sedit --remember FILE   edit FILE and save its password in the macOS Keychain
  sedit --forget FILE     remove FILE's password from the Keychain
  sedit --set-default     store a default password in the macOS Keychain
  sedit --forget-default  remove the default password
  sedit -h, --help        show this help
  sedit --version         show the version

--default/--custom choose the password for a new file without asking.
--share encrypts a copy to age (age1...) or SSH public keys; OUT defaults to
FILE.age ("-" is stdout) and -a writes ASCII armor. Recipients decrypt it with
"age -d -i KEY" and don't need sedit. --import is the reverse: -i names your
age or SSH private key (omit it for a passphrase-encrypted file); OUT defaults
to FILE.age without the .age, and an existing file is never overwritten.
A stored password (per file, then the default) is used instead of prompting.
A symlink is followed: the file it points to is edited, locked and backed up.
The editor is taken from $SEDIT_EDITOR, then $VISUAL, then $EDITOR, then vim or vi.
Graphical editors (VS Code, ...) in $VISUAL or $EDITOR are ignored, because they
keep plaintext copies; set $SEDIT_EDITOR to use one anyway.
Set SEDIT_STATUSLINE=0 to turn off the SEDIT ENCRYPTED status line banner and
watermark that sedit adds to vim.`

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
	case "import":
		return importFile(o.file, o.identities, o.out, o.choice)
	case "share":
		recips, err := collectRecipients(o.to, o.recipFiles)
		if err != nil {
			return err
		}
		return share(o.file, recips, o.out, o.armor)
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
	src := srcPrompt
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
		if password, plain, src, err = openExisting(path, data); err != nil {
			return err
		}
	}
	defer wipe(password)
	defer wipe(plain)
	defer func() {
		if err == nil && remember {
			if err = storePassword(path, password); err == nil {
				fmt.Fprintln(os.Stderr, "sedit: password remembered in Keychain")
			}
		}
	}()

	edited, err := runEditor(path, plain)
	if err != nil {
		return err
	}
	defer wipe(edited)
	if data != nil && bytes.Equal(plain, edited) {
		fmt.Fprintf(os.Stderr, "sedit: no changes to %s\n", path)
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
	if err := writeAtomic(path, out); err != nil {
		return err
	}
	verb := "saved"
	if data == nil {
		verb = "created"
	}
	report(verb, path, src)
	return nil
}

var errNoStore = errors.New("storing passwords isn't supported on this platform")

// pwSource says where the password used to open a file came from.
type pwSource int

const (
	srcPrompt  pwSource = iota // typed by the user
	srcFile                    // the file's own Keychain entry
	srcDefault                 // the shared default password
)

// note describes the source for the user, or "" if it's unremarkable.
func (s pwSource) note() string {
	switch s {
	case srcFile:
		return "password from Keychain"
	case srcDefault:
		return "default password from Keychain"
	}
	return ""
}

// report tells the user what happened to path, e.g.
// "sedit: saved notes.txt (encrypted; password from Keychain)".
func report(verb, path string, src pwSource) {
	detail := "encrypted"
	if n := src.note(); n != "" {
		detail += "; " + n
	}
	fmt.Fprintf(os.Stderr, "sedit: %s %s (%s)\n", verb, path, detail)
}

// openExisting decrypts data, using the file's stored password, then the default
// password, then prompting. A stored password that no longer works is removed.
func openExisting(path string, data []byte) (password, plain []byte, src pwSource, err error) {
	if store != nil {
		if acct, aerr := account(path); aerr == nil {
			pw, gerr := store.Get(acct)
			switch {
			case gerr == nil:
				if plain, err = Decrypt(pw, data); err == nil {
					return pw, plain, srcFile, nil
				}
				wipe(pw)
				if !errors.Is(err, ErrBadPassword) {
					return nil, nil, srcPrompt, err
				}
				store.Delete(acct)
				fmt.Fprintln(os.Stderr, "sedit: stored password no longer works; removed it")
			case !errors.Is(gerr, ErrNotFound):
				fmt.Fprintln(os.Stderr, "sedit: can't read stored password:", gerr)
			}
		}
	}
	if pw, pt, ok, derr := tryDefault(data); derr != nil {
		return nil, nil, srcPrompt, derr
	} else if ok {
		return pw, pt, srcDefault, nil
	}
	if password, err = prompt("Password: "); err != nil {
		return nil, nil, srcPrompt, err
	}
	if plain, err = Decrypt(password, data); err != nil {
		wipe(password)
		return nil, nil, srcPrompt, err
	}
	return password, plain, srcPrompt, nil
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
	if err := writeAtomic(path, out); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "sedit: encrypted %s\n", path)
	return nil
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
	old, plain, src, err := openExisting(path, data)
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
	if src == srcFile {
		if err := storePassword(path, password); err != nil {
			return fmt.Errorf("password changed, but updating the stored one failed (run \"sedit --forget %s\"): %w", path, err)
		}
	}
	fmt.Fprintf(os.Stderr, "sedit: password changed for %s\n", path)
	return nil
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
	line, err := stdinReader().ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	return bytes.TrimRight(line, "\r\n"), nil
}

// stdin is read through one buffered reader, so that several prompts can be
// answered from a single pipe. A fresh reader per prompt would swallow
// everything after the first line.
var stdin struct {
	file   *os.File
	reader *bufio.Reader
}

func stdinReader() *bufio.Reader {
	if stdin.file != os.Stdin { // first use, or os.Stdin was replaced (tests)
		stdin.file, stdin.reader = os.Stdin, bufio.NewReader(os.Stdin)
	}
	return stdin.reader
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
