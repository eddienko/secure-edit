package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
	script := ""
	if statuslineEnabled() {
		script = filepath.Join(dir, "sedit.vim") // removed with dir
		if err := os.WriteFile(script, []byte(vimScript(tempName(name))), 0o600); err != nil {
			return nil, err
		}
	}
	argv := editorCmd(tempName(name), script)
	if isGUIEditor(argv[0]) {
		fmt.Fprintf(os.Stderr, "sedit: using %q; graphical editors may keep plaintext copies in their own history and backups\n", strings.Join(argv, " "))
	}
	argv = append(argv, tmp)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	start := time.Now()
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("editor failed, file not changed: %w", err)
	}
	edited, err = os.ReadFile(tmp)
	if err == nil && bytes.Equal(edited, plain) {
		if d := time.Since(start); d < fastExit {
			// Some editors (e.g. "code" without --wait) hand the file to an
			// already running app and exit at once. Their window is then editing
			// a temp file that is deleted as soon as we return.
			fmt.Fprintf(os.Stderr, "sedit: warning: the editor exited after %d ms without changes.\n"+
				"  If it opened a window and returned immediately, your edits are not being saved:\n"+
				"  set VISUAL to a command that waits, for example VISUAL=\"code --wait\".\n", d.Milliseconds())
		}
	}
	return edited, err
}

// fastExit is how quickly an editor can exit, with no changes, before we
// suspect it detached from the terminal instead of waiting for the user.
const fastExit = time.Second

// waitEditors are graphical editors whose command returns at once unless told
// to wait for the file to be closed. All of them accept --wait.
var waitEditors = map[string]bool{
	"code": true, "code-insiders": true, "codium": true, "vscodium": true,
	"cursor": true, "windsurf": true, "zed": true, "subl": true,
	"atom": true, "mate": true, "bbedit": true,
}

func isGUIEditor(cmd string) bool { return waitEditors[filepath.Base(cmd)] }

func hasWaitFlag(args []string) bool {
	for _, a := range args {
		if a == "--wait" || a == "-w" {
			return true
		}
	}
	return false
}

// statuslineEnabled reports whether the vim status line banner is wanted. It
// is on unless SEDIT_STATUSLINE is set to 0, false, no or off.
func statuslineEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SEDIT_STATUSLINE"))) {
	case "0", "false", "no", "off":
		return false
	}
	return true
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

// editorCmd returns the editor command line. For graphical editors that would
// otherwise return immediately (VS Code, Sublime Text, ...) it adds --wait. For
// vim-family editors it adds
// flags that stop plaintext leaking into swap, backup, undo and viminfo files,
// sets the terminal title, and (if script is not empty) sources script, which
// puts a banner in the status line, so it's obvious this is a sedit session.
func editorCmd(name, script string) []string {
	ed := os.Getenv("VISUAL")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	if ed == "" {
		ed = "vi"
	}
	argv := strings.Fields(ed)
	switch base := filepath.Base(argv[0]); {
	case base == "vi" || base == "vim" || base == "nvim" || base == "view":
		argv = append(argv, vimArgs(name, script)...)
	case waitEditors[base] && !hasWaitFlag(argv[1:]):
		argv = append(argv, "--wait") // otherwise it returns before you edit
	}
	return argv
}

func vimArgs(name, script string) []string {
	args := []string{
		"-n", "-i", "NONE",
		"-c", "set nobackup nowritebackup noundofile noswapfile viminfo=",
		"-c", "set title",
		"-c", "let &titlestring = " + vimString("sedit: "+titleEscape(name)),
	}
	if script != "" {
		args = append(args, "-S", script)
	}
	return args
}

// vimScript returns a vim script that marks the session as a sedit one: a
// banner in the status line, and a small right-aligned watermark on the first
// line (virtual text, so it is never part of the file; needs vim 9.0.0067 or
// newer, or neovim).
//
// The user's vimrc and plugins run before it, so it overrides their settings.
// Status line plugins re-set theirs on window and buffer events, and a
// colorscheme change clears our highlight, so everything is re-applied on the
// same events (our autocommands are registered last, so they run last).
func vimScript(name string) string {
	label := titleEscape(name)
	return `" Generated by sedit: marks this vim session as an encrypted edit.
scriptencoding utf-8
let s:status = ` + vimString("%#SeditBanner# SEDIT ENCRYPTED %* "+label+"%m%r%=%l,%c  %P ") + `
let s:mark = ' sedit · encrypted '
if has('nvim')
  let s:ns = nvim_create_namespace('sedit')
endif
function! s:Mark() abort
  if has('nvim')
    call nvim_buf_clear_namespace(0, s:ns, 0, -1)
    call nvim_buf_set_extmark(0, s:ns, 0, 0, {'virt_text': [[s:mark, 'SeditMark']], 'virt_text_pos': 'right_align'})
  elseif exists('*prop_add') && has('patch-9.0.0067')
    silent! call prop_type_add('sedit_mark', {'highlight': 'SeditMark'})
    call prop_remove({'type': 'sedit_mark', 'all': 1}, 1, line('$'))
    call prop_add(1, 0, {'type': 'sedit_mark', 'text': s:mark, 'text_align': 'right'})
  endif
endfunction
function! s:Apply() abort
  highlight SeditBanner ctermfg=15 ctermbg=24 cterm=bold guifg=#ffffff guibg=#0b2f5e gui=bold
  highlight SeditMark ctermfg=33 cterm=bold guifg=#2380e8 gui=bold
  set laststatus=2
  let &statusline = s:status
  let &l:statusline = s:status
  silent! call s:Mark()
endfunction
augroup sedit
  autocmd!
  autocmd VimEnter,BufEnter,BufWinEnter,WinEnter,ColorScheme * call s:Apply()
  autocmd TextChanged * silent! call s:Mark()
augroup END
call s:Apply()
`
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
