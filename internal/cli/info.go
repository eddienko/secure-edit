package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/eddienko/secure-edit/internal/fileops"
	"github.com/eddienko/secure-edit/internal/vault"
)

// createMeta is the title and description for a file that is being created by
// this run (edit, --encrypt, --write, --append, --import).
var createMeta vault.Meta

// setCreateMeta checks --title and --description and puts them in createMeta.
// They are for new files only: on one that already exists they would be applied
// by a plain edit, which would be surprising, so --set-info is the way.
func setCreateMeta(o opts) error {
	createMeta = vault.Meta{}
	if o.title == nil && o.description == nil || o.mode == "set-info" {
		return nil
	}
	m := vault.Meta{}
	if o.title != nil {
		m.Title = *o.title
	}
	if o.description != nil {
		m.Description = *o.description
	}
	m = m.Clean()
	if err := m.Validate(); err != nil {
		return err
	}
	if o.mode == "edit" || o.mode == "write" || o.mode == "append" {
		if fi, err := os.Stat(o.file); err == nil && fi.Size() > 0 {
			return fmt.Errorf("%s already exists; use \"sedit --set-info %s\" to change its title or description", o.file, o.file)
		}
	}
	createMeta = m
	return nil
}

// info prints a file's title and description. It needs no password: they are in
// the file header. They are not authenticated until the file is opened, so
// treat them as a hint.
func info(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := requireSedit(path, data); err != nil {
		return err
	}
	m, err := vault.ReadMeta(data)
	if err != nil {
		return err
	}
	if m.IsZero() {
		fmt.Fprintf(os.Stderr, "sedit: %s has no title or description\n", path)
		return nil
	}
	if m.Title != "" {
		fmt.Println("Title:", m.Title)
	}
	if m.Description != "" {
		fmt.Println("Description:", strings.ReplaceAll(m.Description, "\n", "\n  "))
	}
	return nil
}

// setInfo changes a file's title and/or description, asking for its password
// like any other change. A part that isn't given is kept; an empty one clears it.
func setInfo(path string, title, description *string) error {
	release, err := vault.Lock(path)
	if err != nil {
		return err
	}
	defer release()

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s doesn't exist", path)
	}
	if err != nil {
		return err
	}
	if err := requireSedit(path, data); err != nil {
		return err
	}
	m, err := vault.ReadMeta(data)
	if err != nil {
		return err
	}
	if title != nil {
		m.Title = *title
	}
	if description != nil {
		m.Description = *description
	}
	m = m.Clean()
	if err := m.Validate(); err != nil {
		return err
	}
	password, plain, _, err := openExisting(path, data)
	if err != nil {
		return err
	}
	defer vault.Wipe(password)
	defer vault.Wipe(plain)
	if err := fileops.SetMeta(path, plain, password, m); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "sedit: updated the title and description of %s\n", path)
	return nil
}
