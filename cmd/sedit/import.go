package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"filippo.io/age/armor"
	"golang.org/x/crypto/ssh"
)

// maxImportSize bounds how much of an age file is read into memory.
const maxImportSize = 256 << 20

const armorHeader = "-----BEGIN AGE ENCRYPTED FILE-----"

// importFile turns an age-encrypted file (for example one made by
// "sedit --share") into a sedit file. The plaintext never touches the disk.
//
// identityFiles are age or SSH private key files. With none, the file must be
// passphrase-encrypted and the passphrase is asked for. out defaults to path
// without its ".age"; an existing file is never overwritten.
func importFile(path string, identityFiles []string, out string, choice pwChoice) error {
	out, err := importOutput(path, out)
	if err != nil {
		return err
	}
	unlock, err := Lock(out)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := os.Lstat(out); err == nil {
		return fmt.Errorf("refusing to overwrite %s", out)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	var ids []age.Identity
	if len(identityFiles) > 0 {
		if ids, err = loadIdentities(identityFiles); err != nil {
			return err
		}
	} else {
		ids = []age.Identity{passphraseIdentity{}}
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxImportSize+1))
	if err != nil {
		return err
	}
	if len(data) > maxImportSize {
		return fmt.Errorf("%s is larger than %d MiB", path, maxImportSize>>20)
	}
	var src io.Reader = bytes.NewReader(data)
	if bytes.HasPrefix(bytes.TrimLeft(data, " \t\r\n"), []byte(armorHeader)) {
		src = armor.NewReader(src)
	}

	r, err := age.Decrypt(src, ids...)
	if err != nil {
		var nm *age.NoIdentityMatchError
		if errors.As(err, &nm) {
			scrypt := false
			for _, t := range nm.StanzaTypes {
				scrypt = scrypt || t == "scrypt"
			}
			switch {
			case scrypt && len(identityFiles) == 0:
				return fmt.Errorf("can't decrypt %s: wrong passphrase", path)
			case scrypt:
				return fmt.Errorf("can't decrypt %s: it is passphrase-encrypted; run --import without -i", path)
			case len(identityFiles) == 0:
				return fmt.Errorf("can't decrypt %s: it isn't passphrase-encrypted; give your private key with -i KEY", path)
			}
			return fmt.Errorf("can't decrypt %s: none of the given keys fit this file (is it addressed to you?)", path)
		}
		return fmt.Errorf("%s is not a usable age file: %w", path, err)
	}
	plain, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("decrypting %s failed (corrupted or truncated?): %w", path, err)
	}
	defer wipe(plain)

	password, err := newPassword(choice)
	if err != nil {
		return err
	}
	defer wipe(password)
	enc, err := Encrypt(password, plain, defaultKDF)
	if err != nil {
		return err
	}
	if err := writeAtomic(out, enc); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "sedit: imported %s as %s (encrypted with a new password)\n", path, out)
	return nil
}

// importOutput decides where an import is written and checks it can't be the
// input itself.
func importOutput(path, out string) (string, error) {
	switch {
	case out == "-":
		return "", errors.New("-o - isn't supported for --import: it creates a file")
	case out == "" && strings.HasSuffix(path, ".age") && len(path) > len(".age"):
		out = strings.TrimSuffix(path, ".age")
	case out == "":
		return "", fmt.Errorf("can't tell where to write: %s doesn't end in .age, so give -o OUT", path)
	}
	if err := refuseSameFile(out, path); err != nil {
		return "", fmt.Errorf("the output %s is the file being imported", out)
	}
	return out, nil
}

// loadIdentities reads age identity files (AGE-SECRET-KEY-...) and SSH private
// keys. Errors never include key material.
func loadIdentities(files []string) ([]age.Identity, error) {
	var ids []age.Identity
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		if bytes.Contains(data, []byte("-----BEGIN")) {
			// data is not wiped: a passphrase-protected key is only decrypted
			// later, from these bytes.
			id, err := parseSSHIdentity(name, data)
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
			continue
		}
		parsed, err := age.ParseIdentities(bytes.NewReader(data))
		wipe(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		ids = append(ids, parsed...)
	}
	return ids, nil
}

// parseSSHIdentity parses an SSH private key. If it is passphrase-protected,
// the passphrase is only asked for if the file turns out to be addressed to it.
func parseSSHIdentity(name string, pem []byte) (age.Identity, error) {
	id, err := agessh.ParseIdentity(pem)
	if err == nil {
		return id, nil
	}
	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	pub := missing.PublicKey
	if pub == nil { // older PEM formats don't carry it; look for NAME.pub
		pubData, perr := os.ReadFile(name + ".pub")
		if perr != nil {
			return nil, fmt.Errorf("%s is passphrase-protected and has no public key inside; create %s.pub next to it", name, name)
		}
		if pub, _, _, _, perr = ssh.ParseAuthorizedKey(pubData); perr != nil {
			return nil, fmt.Errorf("%s.pub: %w", name, perr)
		}
	}
	return agessh.NewEncryptedSSHIdentity(pub, pem, func() ([]byte, error) {
		return prompt("Passphrase for " + name + ": ")
	})
}

// passphraseIdentity decrypts passphrase-encrypted age files. It asks for the
// passphrase only if the file actually is one.
type passphraseIdentity struct{}

func (passphraseIdentity) Unwrap(stanzas []*age.Stanza) ([]byte, error) {
	if len(stanzas) != 1 || stanzas[0].Type != "scrypt" {
		return nil, age.ErrIncorrectIdentity
	}
	pass, err := prompt("Passphrase: ")
	if err != nil {
		return nil, err
	}
	defer wipe(pass)
	id, err := age.NewScryptIdentity(string(pass))
	if err != nil {
		return nil, err
	}
	return id.Unwrap(stanzas)
}
