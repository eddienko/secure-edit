package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// writeStdin stores data piped in on stdin in a sedit file: as its whole
// contents, or added to the end if appendMode is set. The file is created if
// it doesn't exist. Stdin carries the data, so passwords come from the
// Keychain or are asked for on the terminal.
func writeStdin(path string, appendMode bool, choice pwChoice) error {
	if stdinIsTerminal() {
		return errors.New("the data is read from stdin, but stdin is a terminal; pipe it in, e.g. echo text | sedit --write FILE")
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, maxImportSize+1))
	if err != nil {
		return err
	}
	defer wipe(input)
	if len(input) > maxImportSize {
		return fmt.Errorf("input is larger than %d MiB", maxImportSize>>20)
	}
	if len(input) == 0 {
		// Likely a failed command upstream: don't wipe the file with nothing.
		if appendMode {
			return errors.New("nothing was piped in, so there is nothing to append")
		}
		return errors.New("nothing was piped in; refusing to replace the contents of " + path + " with nothing")
	}

	// From here on stdin is spent, so passwords must be asked on the terminal.
	stdinIsData = true
	defer func() { stdinIsData = false }()
	if tty, err := openTTY(); err == nil {
		promptTTY = tty
		defer func() { promptTTY = nil; tty.Close() }()
	}

	unlock, err := Lock(path)
	if err != nil {
		return err
	}
	defer unlock()

	prev, err := os.ReadFile(path)
	var password, plain []byte
	src := srcPrompt
	switch {
	case errors.Is(err, fs.ErrNotExist), err == nil && len(prev) == 0:
		prev = nil // new or empty file: nothing to back up
		if password, err = newPassword(choice); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if err := requireSedit(path, prev); err != nil {
			return err
		}
		if password, plain, src, err = openExisting(path, prev); err != nil {
			return err
		}
	}
	defer wipe(password)
	defer wipe(plain)

	content := input
	if appendMode {
		content = append(bytes.Clone(plain), input...)
		defer wipe(content)
	}
	return saveFile(path, password, content, prev, src)
}
