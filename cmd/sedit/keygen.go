package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"filippo.io/age"
)

// keygen creates an age key pair in the same file format as age-keygen:
//
//	# created: 2026-10-09T15:30:00+01:00
//	# public key: age1...
//	AGE-SECRET-KEY-1...
//
// The file is created with mode 0600 and never replaces an existing one. out
// "-" writes to stdout instead. pq makes a post-quantum hybrid key.
func keygen(out string, pq bool) error {
	var secret, public string
	if pq {
		id, err := age.GenerateHybridIdentity()
		if err != nil {
			return err
		}
		secret, public = id.String(), id.Recipient().String()
	} else {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			return err
		}
		secret, public = id.String(), id.Recipient().String()
	}
	content := fmt.Sprintf("# created: %s\n# public key: %s\n%s\n", time.Now().Format(time.RFC3339), public, secret)

	if out == "-" {
		if _, err := fmt.Fprint(os.Stdout, content); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "sedit: the private key was written to stdout; keep it secret")
	} else if err := writeNewPrivate(out, content); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Public key: %s\n", public)
	if out != "-" {
		fmt.Fprintf(os.Stderr, "sedit: private key saved to %s (keep it secret)\n", out)
	}
	fmt.Fprintf(os.Stderr, "  Give the public key to people who want to send you files.\n"+
		"  To open what they send:  sedit --import FILE.age -i %s\n", keyHint(out))
	return nil
}

func keyHint(out string) string {
	if out == "-" {
		return "KEYFILE"
	}
	return out
}

// writeNewPrivate creates path with mode 0600 and writes content to it. It
// fails if the path already exists (including as a symlink) instead of
// replacing it, and removes the file again if writing fails.
func writeNewPrivate(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("refusing to overwrite %s", path)
	}
	if err != nil {
		return err
	}
	_, err = f.WriteString(content)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		return err
	}
	return nil
}
