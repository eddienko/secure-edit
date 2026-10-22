// Package share encrypts text to other people's public keys in the age format,
// so they can read it with plain "age -d". The command line (--share) and the
// interface (x) both use it.
package share

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"filippo.io/age/armor"
)

// ErrPrivateKey is returned for a private key, which must never be echoed back.
var ErrPrivateKey = errors.New("that is a private key; give the recipient's public key")

// ParseRecipient understands the public keys the age tool accepts, except
// plugins and tagged recipients, which would need external programs or have
// different privacy properties: age X25519 keys (age1...), post-quantum hybrid
// keys (age1pq1...) and SSH public keys (ssh-ed25519, ssh-rsa).
func ParseRecipient(s string) (age.Recipient, error) {
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
		return nil, ErrPrivateKey
	}
	return nil, errors.New("not an age (age1...) or SSH (ssh-ed25519, ssh-rsa) public key")
}

// ReadRecipients reads a recipients file: one public key per line, with blank
// lines and lines starting with "#" ignored. An SSH public key file such as
// ~/.ssh/id_ed25519.pub works as it is.
func ReadRecipients(path string) ([]age.Recipient, error) {
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
		r, err := ParseRecipient(line)
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

// ParseList reads recipients typed or pasted as text: one per line, each a public
// key or the path of a file of keys (a leading ~ is the home folder). Blank lines
// and lines starting with "#" are ignored.
func ParseList(text string) ([]age.Recipient, error) {
	var recs []age.Recipient
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if isKey(line) {
			r, err := ParseRecipient(line)
			if errors.Is(err, ErrPrivateKey) {
				return nil, err // don't quote any part of it
			}
			if err != nil {
				return nil, fmt.Errorf("invalid recipient %q: %w", Shorten(line), err)
			}
			recs = append(recs, r)
			continue
		}
		rs, err := ReadRecipients(expandHome(line))
		if err != nil {
			return nil, fmt.Errorf("%q is not a public key, and not a readable file of keys: %w", Shorten(line), unwrapPath(err))
		}
		if len(rs) == 0 {
			return nil, fmt.Errorf("%s: no recipients found", line)
		}
		recs = append(recs, rs...)
	}
	if len(recs) == 0 {
		return nil, errors.New("give at least one public key, or the path of a file of keys")
	}
	return recs, nil
}

// isKey says whether a line is meant as a key rather than as a path.
func isKey(s string) bool {
	for _, p := range []string{"age1", "ssh-", "AGE-SECRET-KEY-", "-----BEGIN"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// unwrapPath drops the path from a file error: it is already named.
func unwrapPath(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// Shorten cuts a long string for an error message.
func Shorten(s string) string {
	if len(s) > 40 {
		return s[:37] + "..."
	}
	return s
}

// Encrypt returns plain encrypted to the recipients: binary age data, or with
// armored text for pasting into an email or a chat.
func Encrypt(plain []byte, recips []age.Recipient, armored bool) ([]byte, error) {
	var buf bytes.Buffer
	var dst io.Writer = &buf
	var aw io.WriteCloser
	if armored {
		aw = armor.NewWriter(&buf)
		dst = aw
	}
	w, err := age.Encrypt(dst, recips...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plain); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	if aw != nil {
		if err := aw.Close(); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}
