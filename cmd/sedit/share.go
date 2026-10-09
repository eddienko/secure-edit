package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"filippo.io/age/armor"
	"golang.org/x/term"
)

// parseRecipient understands the public keys the age tool accepts, except
// plugins and tagged recipients, which would need external programs or have
// different privacy properties: age X25519 keys (age1...), post-quantum hybrid
// keys (age1pq1...) and SSH public keys (ssh-ed25519, ssh-rsa).
// errPrivateKey is returned for a private key, which must never be echoed back.
var errPrivateKey = errors.New("that is a private key; give the recipient's public key")

func parseRecipient(s string) (age.Recipient, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return nil, errors.New("empty recipient")
	case strings.HasPrefix(s, "age1pq1"):
		return age.ParseHybridRecipient(s)
	case strings.HasPrefix(s, "age1"):
		return age.ParseX25519Recipient(s)
	case strings.HasPrefix(s, "ssh-"):
		return agessh.ParseRecipient(s)
	case strings.HasPrefix(s, "AGE-SECRET-KEY-"), strings.HasPrefix(s, "-----BEGIN"):
		return nil, errPrivateKey
	}
	return nil, errors.New("not an age (age1...) or SSH (ssh-ed25519, ssh-rsa) public key")
}

// readRecipients reads a recipients file: one public key per line, with blank
// lines and lines starting with "#" ignored. An SSH public key file such as
// ~/.ssh/id_ed25519.pub works as it is.
func readRecipients(path string) ([]age.Recipient, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var recs []age.Recipient
	sc := bufio.NewScanner(io.LimitReader(f, 1<<20))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r, err := parseRecipient(line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		recs = append(recs, r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return recs, nil
}

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

func shorten(s string) string {
	if len(s) > 40 {
		return s[:37] + "..."
	}
	return s
}

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
	defer wipe(password)
	defer wipe(plain)

	var buf bytes.Buffer
	var dst io.Writer = &buf
	var aw io.WriteCloser
	if armored {
		aw = armor.NewWriter(&buf)
		dst = aw
	}
	w, err := age.Encrypt(dst, recips...)
	if err != nil {
		return err
	}
	if _, err := w.Write(plain); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	if aw != nil {
		if err := aw.Close(); err != nil {
			return err
		}
	}

	if toStdout {
		if _, err := os.Stdout.Write(buf.Bytes()); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "sedit: shared %s with %s (written to stdout)\n", path, plural(len(recips), "recipient"))
		return nil
	}
	if err := writeAtomic(out, buf.Bytes()); err != nil {
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
