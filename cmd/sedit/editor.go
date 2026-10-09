package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runEditor writes plain to a private temp file named after the real file
// (so editors show the right name and pick the right syntax highlighting),
// runs the editor on it, and returns the resulting contents. The temp file is
// zeroed and removed after.
func runEditor(name string, plain []byte) (edited []byte, err error) {
	dir, err := os.MkdirTemp("", "sedit-") // mode 0700
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	tmp := filepath.Join(dir, tempName(name))
	defer shred(tmp)

	if err := os.WriteFile(tmp, plain, 0o600); err != nil {
		return nil, err
	}
	argv := editorCmd(tempName(name))
	argv = append(argv, tmp)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("editor failed, file not changed: %w", err)
	}
	return os.ReadFile(tmp)
}

// tempName is the file name used for the plaintext temp file: the base name of
// the real file, or "secret.txt" if that isn't usable as a plain file name.
func tempName(path string) string {
	base := filepath.Base(path)
	switch base {
	case "", ".", "..", string(filepath.Separator):
		return "secret.txt"
	}
	return base
}

// editorCmd returns the editor command line. For vim-family editors it adds
// flags that stop plaintext leaking into swap, backup, undo and viminfo files,
// and sets the terminal title so it's obvious this is a sedit session.
func editorCmd(name string) []string {
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
		argv = append(argv, vimArgs(name)...)
	}
	return argv
}

func vimArgs(name string) []string {
	return []string{
		"-n", "-i", "NONE",
		"-c", "set nobackup nowritebackup noundofile noswapfile viminfo=",
		"-c", "set title",
		"-c", "let &titlestring = " + vimString("sedit: "+titleEscape(name)),
	}
}

// titleEscape makes s safe to show literally in vim's 'titlestring', where
// '%' starts a format item. Control characters are dropped.
func titleEscape(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return strings.ReplaceAll(s, "%", "%%")
}

// vimString quotes s as a vim single-quoted string literal.
func vimString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
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
