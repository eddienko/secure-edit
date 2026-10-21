package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/eddienko/secure-edit/internal/editor"
	"github.com/eddienko/secure-edit/internal/fileops"
	"github.com/eddienko/secure-edit/internal/keystore"
	"github.com/eddienko/secure-edit/internal/unlock"
	"github.com/eddienko/secure-edit/internal/vault"
	"golang.org/x/term"
)

const usage = `usage:
  sedit [--default|--custom] FILE
                          edit FILE (created if it doesn't exist or is empty)
  sedit -p FILE           decrypt FILE to stdout
  sedit --info FILE       show FILE's title and description (no password needed)
  sedit --set-info FILE [--title TEXT] [--description TEXT]
                          change FILE's title and description
  sedit [--default|--custom] --passwd FILE
                          change FILE's password
  sedit [--default|--custom] --encrypt [-y] FILE
                          encrypt an existing plaintext FILE in place
  sedit [--default|--custom] --write FILE
                          make FILE's contents whatever is piped in on stdin
  sedit [--default|--custom] --append FILE
                          add whatever is piped in to the end of FILE
  sedit --share FILE (--to KEY | -R FILE)... [-o OUT] [-a]
                          write a copy of FILE for others to decrypt with "age"
  sedit [--default|--custom] --import FILE.age [-i KEY]... [-o OUT]
                          turn an age-encrypted FILE.age into a sedit file
  sedit --keygen -o FILE [--pq]
                          create an age key pair, for receiving shared files
  sedit --mv OLD NEW      move a file, with its backup and stored password
  sedit --rm [-y] FILE... delete files, with their backups and stored passwords
  sedit --remember FILE   edit FILE and save its password in the macOS Keychain
  sedit --forget FILE     remove FILE's password from the Keychain
  sedit --set-default     store a default password in the macOS Keychain
  sedit --forget-default  remove the default password
  sedit -h, --help        show this help
  sedit --version         show the version

--default/--custom choose the password for a new file without asking.
--title and --description give a file you create (FILE, --encrypt, --write,
--append, --import) a title and description. They are stored in the clear, so
they can be shown without the password (sedit-tui does): don't put secrets in
them. Files that have them need sedit 1.4 or newer.
--write and --append take the data from stdin and ask for the password on the
terminal (or use the Keychain); they never overwrite a file with empty input.
--share encrypts a copy to age (age1...) or SSH public keys; OUT defaults to
FILE.age ("-" is stdout) and -a writes ASCII armor. Recipients decrypt it with
"age -d -i KEY" and don't need sedit. --import is the reverse: -i names your
age or SSH private key (omit it for a passphrase-encrypted file); OUT defaults
to FILE.age without the .age, and an existing file is never overwritten.
--mv and --rm also move or delete FILE.bak and the password remembered for the
file, which a plain mv or rm would leave behind. They refuse symlinks, never
overwrite, and --rm asks first (-y to skip). It is not a secure erase.
--keygen writes a new private key to FILE (mode 0600, never overwriting) and
prints the public key to give to senders.
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
	// --mv and --rm deal with symlinks themselves: they must not follow them.
	if o.file != "" && o.mode != "mv" && o.mode != "rm" {
		if o.file, err = vault.ResolvePath(o.file); err != nil {
			return err
		}
	}
	if err := setCreateMeta(o); err != nil {
		return err
	}
	switch o.mode {
	case "info":
		return info(o.file)
	case "set-info":
		return setInfo(o.file, o.title, o.description)
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
	case "mv":
		return mv(o.file, o.dest)
	case "rm":
		return rm(o.files, o.yes)
	case "write":
		return writeStdin(o.file, false, o.choice)
	case "append":
		return writeStdin(o.file, true, o.choice)
	case "keygen":
		return keygen(o.out, o.pq)
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
	if remember && keystore.Backend == nil {
		return keystore.ErrNoBackend
	}
	release, err := vault.Lock(path)
	if err != nil {
		return err
	}
	defer release()

	data, err := os.ReadFile(path)
	var password, plain []byte
	src := unlock.SrcPrompt
	switch {
	case errors.Is(err, fs.ErrNotExist), err == nil && len(data) == 0:
		// New or empty file: start fresh, and there's nothing to back up.
		data = nil
		stdinUsedForPassword = false
		if password, err = newPassword(choice); err != nil {
			return err
		}
		if stdinUsedForPassword {
			fmt.Fprintln(os.Stderr, "sedit: warning: stdin is not a terminal, so it was used as the new password.\n"+
				"  To store piped data in a file, use --write or --append.")
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
	defer vault.Wipe(password)
	defer vault.Wipe(plain)
	defer func() {
		if err == nil && remember {
			if err = unlock.StorePassword(path, password); err == nil {
				fmt.Fprintln(os.Stderr, "sedit: password remembered in Keychain")
			}
		}
	}()

	edited, err := editor.Run(path, plain)
	if err != nil {
		return err
	}
	defer vault.Wipe(edited)
	if data != nil && bytes.Equal(plain, edited) {
		fmt.Fprintf(os.Stderr, "sedit: no changes to %s\n", path)
		return nil
	}
	return saveFile(path, password, edited, data, src)
}

// saveFile encrypts content under password and writes it to path. prev is the
// existing ciphertext, kept as FILE.bak, or nil for a new file.
func saveFile(path string, password, content, prev []byte, src unlock.Source) error {
	var err error
	if prev == nil {
		err = vault.SaveMeta(path, password, content, nil, createMeta)
	} else {
		err = vault.Save(path, password, content, prev)
	}
	if err != nil {
		return err
	}
	verb := "saved"
	if prev == nil {
		verb = "created"
	}
	report(verb, path, src)
	return nil
}

// report tells the user what happened to path, e.g.
// "sedit: saved notes.txt (encrypted; password from Keychain)".
func report(verb, path string, src unlock.Source) {
	detail := "encrypted"
	if n := src.Note(); n != "" {
		detail += "; " + n
	}
	fmt.Fprintf(os.Stderr, "sedit: %s %s (%s)\n", verb, path, detail)
}

// openExisting decrypts data with the file's stored password, then the default
// password, and failing that asks on the prompt.
func openExisting(path string, data []byte) (password, plain []byte, src unlock.Source, err error) {
	return unlock.Open(path, data, prompt, func(msg string) { fmt.Fprintln(os.Stderr, "sedit:", msg) })
}

func forget(path string) error {
	if keystore.Backend == nil {
		return keystore.ErrNoBackend
	}
	acct, err := keystore.Account(path)
	if err != nil {
		return err
	}
	if err := keystore.Backend.Delete(acct); errors.Is(err, keystore.ErrNotFound) {
		return fmt.Errorf("no stored password for %s", path)
	} else if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "removed stored password for", path)
	return nil
}

// requireSedit fails early, before any password prompt, if data isn't a sedit file.
func requireSedit(path string, data []byte) error {
	if vault.IsSedit(data) {
		return nil
	}
	return fmt.Errorf("%s is not a sedit file (use \"sedit --encrypt %s\" to convert a plaintext file)", path, path)
}

// encrypt converts an existing plaintext file to a sedit file in place.
func encrypt(path string, yes bool, choice pwChoice) error {
	release, err := vault.Lock(path)
	if err != nil {
		return err
	}
	defer release()

	plain, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	defer vault.Wipe(plain)
	if vault.IsSedit(plain) {
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
	defer vault.Wipe(password)
	out, err := vault.EncryptMeta(password, plain, vault.DefaultKDF, createMeta)
	if err != nil {
		return err
	}
	// No .bak here: it would be a copy of the plaintext.
	if err := vault.WriteAtomic(path, out); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "sedit: encrypted %s\n", path)
	return nil
}

func confirm(msg string) error {
	if !stdinIsTerminal() {
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
	defer vault.Wipe(password)
	defer vault.Wipe(plain)
	_, err = os.Stdout.Write(plain)
	return err
}

func passwd(path string, choice pwChoice) error {
	release, err := vault.Lock(path)
	if err != nil {
		return err
	}
	defer release()

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
	defer vault.Wipe(old)
	defer vault.Wipe(plain)
	password, err := newPassword(choice)
	if err != nil {
		return err
	}
	defer vault.Wipe(password)
	res, err := fileops.Rekey(path, plain, password, src == unlock.SrcFile)
	warnAll(res)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "sedit: password changed for %s\n", path)
	return nil
}

func prompt(msg string) ([]byte, error) {
	if f, ok := promptTerminal(); ok {
		fmt.Fprint(os.Stderr, msg)
		pw, err := readSecret(int(f.Fd()))
		fmt.Fprintln(os.Stderr)
		return pw, err
	}
	if stdinIsData {
		return nil, errNoTerminal
	}
	// Non-interactive: read one line from stdin (useful for scripting/tests).
	line, err := stdinReader().ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	stdinUsedForPassword = true
	return bytes.TrimRight(line, "\r\n"), nil
}

// Where passwords are asked: on the terminal if stdin is one; otherwise one line
// is read from stdin (scripting). --write and --append use stdin for data, so
// they ask on the controlling terminal (/dev/tty) instead, and when there is
// none there is no way to ask.
var (
	promptTTY            *os.File // set while stdin carries data
	stdinIsData          bool
	stdinUsedForPassword bool // a password was read from a pipe

	// Replaceable in tests.
	readSecret      = term.ReadPassword
	openTTY         = func() (*os.File, error) { return os.OpenFile("/dev/tty", os.O_RDWR, 0) }
	stdinIsTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
)

var errNoTerminal = errors.New("no terminal to ask for the password on, because stdin is carrying the data; " +
	"store the password in the Keychain (--remember) or set a default password (--set-default)")

// promptTerminal returns the terminal to ask on, if there is one.
func promptTerminal() (*os.File, bool) {
	if promptTTY != nil {
		return promptTTY, true
	}
	if !stdinIsData && stdinIsTerminal() {
		return os.Stdin, true
	}
	return nil, false
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
	if _, ok := promptTerminal(); !ok {
		return a, nil // scripting: no confirmation
	}
	b, err := prompt("Confirm password: ")
	if err != nil {
		return nil, err
	}
	defer vault.Wipe(b)
	if !bytes.Equal(a, b) {
		return nil, errors.New("passwords don't match")
	}
	return a, nil
}
