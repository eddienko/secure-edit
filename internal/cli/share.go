package cli

import (
	"errors"
	"fmt"
	"os"

	"filippo.io/age"
	shr "github.com/eddienko/secure-edit/internal/share"
	"github.com/eddienko/secure-edit/internal/vault"
	"golang.org/x/term"
)

var errPrivateKey = shr.ErrPrivateKey

func parseRecipient(s string) (age.Recipient, error) { return shr.ParseRecipient(s) }

func readRecipients(path string) ([]age.Recipient, error) { return shr.ReadRecipients(path) }

// collectRecipients parses the --to values and the -R files.
func collectRecipients(to, files []string) ([]age.Recipient, error) {
	var recs []age.Recipient
	for _, s := range to {
		r, err := parseRecipient(s)
		if errors.Is(err, errPrivateKey) {
			return nil, err // don't quote any part of it
		}
		if err != nil {
			return nil, fmt.Errorf("invalid recipient %q: %w", shorten(s), err)
		}
		recs = append(recs, r)
	}
	for _, f := range files {
		rs, err := readRecipients(f)
		if err != nil {
			return nil, err
		}
		if len(rs) == 0 {
			return nil, fmt.Errorf("%s: no recipients found", f)
		}
		recs = append(recs, rs...)
	}
	if len(recs) == 0 {
		return nil, errors.New("--share needs at least one recipient (--to KEY or -R FILE)")
	}
	return recs, nil
}

func shorten(s string) string { return shr.Shorten(s) }

// share decrypts a sedit file in memory and writes a copy encrypted to the
// given recipients in the age format, so they can read it with plain "age -d".
// The plaintext never touches the disk. out is a file name, "-" for stdout, or
// "" for path + ".age".
func share(path string, recips []age.Recipient, out string, armored bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := requireSedit(path, data); err != nil {
		return err
	}
	if out == "" {
		out = path + ".age"
	}
	toStdout := out == "-"
	if toStdout && !armored && term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("refusing to write binary data to a terminal; use -a (armor) or -o FILE")
	}
	if !toStdout {
		if err := refuseSameFile(out, path); err != nil {
			return err
		}
	}

	password, plain, _, err := openExisting(path, data)
	if err != nil {
		return err
	}
	defer vault.Wipe(password)
	defer vault.Wipe(plain)

	out2, err := shr.Encrypt(plain, recips, armored)
	if err != nil {
		return err
	}

	if toStdout {
		if _, err := os.Stdout.Write(out2); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "sedit: shared %s with %s (written to stdout)\n", path, plural(len(recips), "recipient"))
		return nil
	}
	if err := vault.WriteAtomic(out, out2); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "sedit: shared %s with %s: %s\n  they can decrypt it with: age -d -i <their key> %s\n",
		path, plural(len(recips), "recipient"), out, out)
	return nil
}

// refuseSameFile stops --share from overwriting the sedit file it reads from,
// including through a symlink or a different spelling of the same path.
func refuseSameFile(out, src string) error {
	if out == src {
		return fmt.Errorf("the output %s is the file being shared", out)
	}
	o, err := os.Stat(out)
	if err != nil {
		return nil // doesn't exist yet
	}
	if s, err := os.Stat(src); err == nil && os.SameFile(o, s) {
		return fmt.Errorf("the output %s is the file being shared", out)
	}
	return nil
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
