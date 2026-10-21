package editor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// capture runs f and returns what it wrote to os.Stderr.
func capture(t *testing.T, f func()) string {
	t.Helper()
	tmp, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = tmp
	defer func() { os.Stderr = old; tmp.Close() }()
	f()
	b, _ := os.ReadFile(tmp.Name())
	return string(b)
}

// script writes an executable shell script and returns its path.
func script(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// useEditor points sedit at an editor and clears the other settings.
func useEditor(t *testing.T, ed string) {
	t.Helper()
	t.Setenv("SEDIT_EDITOR", ed)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	t.Setenv("SEDIT_STATUSLINE", "")
}

func TestTempName(t *testing.T) {
	for in, want := range map[string]string{
		"secret.txt":         "secret.txt",
		"/a/b/notes.md":      "notes.md",
		"rel/dir/todo.yaml":  "todo.yaml",
		"no-extension":       "no-extension",
		"/":                  "secret.txt",
		".":                  "secret.txt",
		"..":                 "secret.txt",
		"":                   "secret.txt",
		"/path/with space.t": "with space.t",
	} {
		if got := tempName(in); got != want {
			t.Errorf("tempName(%q) = %q, want %q", in, got, want)
		}
	}
}

func mustCmd(name, script string) []string {
	argv, _ := editorCmd(name, script)
	return argv
}

func TestTitleHelpers(t *testing.T) {
	if got := titleEscape("50%\nx\x00.txt"); got != "50%%x.txt" {
		t.Errorf("titleEscape: %q", got)
	}
	if got := vimString("it's"); got != "'it''s'" {
		t.Errorf("vimString: %q", got)
	}
}

func TestEditorCmdOnlyAddsVimFlagsForVim(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim")
	if got := strings.Join(mustCmd("notes.md", ""), " "); !strings.Contains(got, "titlestring") || !strings.Contains(got, "noswapfile") {
		t.Errorf("vim: %q", got)
	}
	t.Setenv("EDITOR", "nano -w")
	if got := mustCmd("notes.md", ""); len(got) != 2 || got[0] != "nano" {
		t.Errorf("nano: %q", got)
	}
}

// The real test of the title quoting: ask vim what it ended up with.
func TestVimTitleWithAwkwardNames(t *testing.T) {
	vim, err := exec.LookPath("vim")
	if err != nil {
		t.Skip("vim not installed")
	}
	dir := t.TempDir()
	for _, name := range []string{
		"secret.txt", "my file.txt", "it's.txt", "50%.txt", `a|b.txt`, `a"b.txt`, `back\slash.txt`, "#hash.txt",
	} {
		out := filepath.Join(dir, "out")
		os.Remove(out)
		file := filepath.Join(dir, "plain.txt")
		os.WriteFile(file, []byte("x\n"), 0o600)
		args := append([]string{"-es", "-u", "NONE"}, vimArgs(name, "")...)
		args = append(args,
			"-c", "call writefile([&titlestring, &title ? 'on' : 'off'], '"+out+"')",
			"-c", "qa!", file)
		if b, err := exec.Command(vim, args...).CombinedOutput(); err != nil {
			t.Fatalf("%q: vim failed: %v\n%s", name, err, b)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("%q: no output: %v", name, err)
		}
		want := "sedit: " + titleEscape(name) + "\non\n"
		if string(b) != want {
			t.Errorf("%q: got %q, want %q", name, b, want)
		}
	}
}

// runVim runs vim headless with sedit's args and script on a throwaway file,
// then evaluates the given Ex commands and returns what writefile() produced.
func runVim(t *testing.T, name string, ex ...string) string {
	t.Helper()
	vim, err := exec.LookPath("vim")
	if err != nil {
		t.Skip("vim not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "sedit.vim")
	if err := os.WriteFile(script, []byte(vimScript(name)), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "plain.txt")
	os.WriteFile(file, []byte("x\n"), 0o600)
	args := append([]string{"-es", "-u", "NONE"}, vimArgs(name, script)...)
	// vim accepts at most 10 -c commands, so run the checks as one.
	args = append(args, "-c", strings.Join(ex, " | "), "-c", "qa!", file)
	if b, err := exec.Command(vim, args...).CombinedOutput(); err != nil {
		t.Fatalf("vim failed: %v\n%s", err, b)
	}
	b, err := os.ReadFile(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatalf("no output: %v", err)
	}
	return string(b)
}

func TestVimBanner(t *testing.T) {
	for _, name := range []string{"notes.md", "my file.txt", "it's.txt", "50%.txt", `a|b.txt`, `a"b.txt`, `back\slash.txt`} {
		out := runVim(t, name,
			"call writefile([&statusline, &laststatus, &showtabline, &fillchars, hlexists('SeditBanner') ? 'hl' : 'nohl'], expand('%:h') . '/out')")
		lines := strings.Split(out, "\n")
		if want := "%#SeditBanner# SEDIT ENCRYPTED %* " + titleEscape(name) + "%m%r%=%l,%c  %P "; lines[0] != want {
			t.Errorf("%q: statusline got %q, want %q", name, lines[0], want)
		}
		if lines[1] != "2" {
			t.Errorf("%q: laststatus = %q", name, lines[1])
		}
		// Everything else about the display is the user's own.
		if lines[2] != "1" {
			t.Errorf("%q: showtabline = %q, sedit must not touch the tab line", name, lines[2])
		}
		if !strings.Contains(lines[3], "eob:~") || strings.Contains(lines[3], "·") {
			t.Errorf("%q: fillchars %q, sedit must leave vim's default end-of-buffer filler alone", name, lines[3])
		}
		if lines[4] != "hl" {
			t.Errorf("%q: highlight missing", name)
		}
	}
}

// Plugins and colorschemes must not be able to remove the banner.
func TestVimBannerSurvivesOverrides(t *testing.T) {
	out := runVim(t, "notes.md",
		"set statusline=PLUGIN laststatus=0",
		"doautocmd WinEnter",
		"let a = [&statusline, &laststatus]",
		"colorscheme default",
		"let b = [synIDattr(hlID('SeditBanner'), 'bg', 'cterm'), synIDattr(hlID('SeditMark'), 'fg', 'cterm')]",
		"call writefile(a + b, expand('%:h') . '/out')")
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[0], "SEDIT ENCRYPTED") {
		t.Errorf("status line overridden: %q", lines[0])
	}
	if lines[1] != "2" {
		t.Errorf("laststatus=%q", lines[1])
	}
	// After the colorscheme change the banner and watermark colours are back.
	if lines[2] != "24" || lines[3] != "33" {
		t.Errorf("highlights after colorscheme: banner bg=%q watermark fg=%q", lines[2], lines[3])
	}
}

func TestVimWatermark(t *testing.T) {
	out := runVim(t, "notes.md",
		"let All = {-> prop_list(1, {'end_lnum': line('$')})}",
		"let a = [len(All()), All()[0].lnum, All()[0].type, &modified]",
		// Inserting a line above, or deleting line 1, moves or loses the mark;
		// after TextChanged there must be exactly one, on line 1.
		"execute 'normal! ggOnew'",
		"doautocmd TextChanged",
		"let b = [len(All()), All()[0].lnum]",
		"execute 'normal! ggdd'",
		"doautocmd TextChanged",
		"let c = [len(All()), All()[0].lnum]",
		"call writefile(map(a + b + c, 'string(v:val)'), expand('%:h') . '/out')")
	got := strings.Join(strings.Split(strings.TrimSpace(out), "\n"), " ")
	// a: 1 mark on line 1, type, not modified by the mark itself.
	// b, c: still exactly one mark, on line 1.
	want := `1 1 'sedit_mark' 0 1 1 1 1`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The watermark is virtual text: it must never end up in the file.
func TestVimWatermarkNotWritten(t *testing.T) {
	vim, err := exec.LookPath("vim")
	if err != nil {
		t.Skip("vim not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "sedit.vim")
	os.WriteFile(script, []byte(vimScript("notes.md")), 0o600)
	file := filepath.Join(dir, "plain.txt")
	os.WriteFile(file, []byte("hello\n"), 0o600)
	args := append([]string{"-es", "-u", "NONE"}, vimArgs("notes.md", script)...)
	args = append(args, "-c", "execute 'normal! Aworld' | write | qa!", file)
	if b, err := exec.Command(vim, args...).CombinedOutput(); err != nil {
		t.Fatalf("vim failed: %v\n%s", err, b)
	}
	if b, _ := os.ReadFile(file); string(b) != "helloworld\n" {
		t.Errorf("file contains %q", b)
	}
}

// Graphical editors are only used when asked for explicitly via SEDIT_EDITOR.
func TestEditorCmdAddsWaitForGUIEditors(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	for ed, want := range map[string]string{
		"code":                         "code --wait",
		"code --wait":                  "code --wait",
		"code -w":                      "code -w",
		"code-insiders --reuse-window": "code-insiders --reuse-window --wait",
		"/usr/local/bin/subl":          "/usr/local/bin/subl --wait",
		"cursor":                       "cursor --wait",
		"zed":                          "zed --wait",
		"nano -w":                      "nano -w", // not a GUI editor: -w means something else
		"emacs -nw":                    "emacs -nw",
	} {
		t.Setenv("SEDIT_EDITOR", ed)
		if got := strings.Join(mustCmd("notes.md", ""), " "); got != want {
			t.Errorf("SEDIT_EDITOR=%q: got %q, want %q", ed, got, want)
		}
	}
}

func TestChooseEditor(t *testing.T) {
	fb := fallbackEditor()
	for _, tc := range []struct {
		sedit, visual, editor string
		want                  string
		noteHas               []string // substrings; nil means no note
	}{
		{"", "", "", fb, nil},
		{"", "vim", "", "vim", nil},
		{"", "", "nano", "nano", nil},
		{"", "emacs -nw", "nano", "emacs -nw", nil}, // VISUAL before EDITOR
		{"nano", "vim", "vim", "nano", nil},         // SEDIT_EDITOR wins
		{"code", "", "", "code", nil},               // explicit opt-in to a GUI editor
		{"  ", "nano", "", "nano", nil},             // blank SEDIT_EDITOR is unset
		{"", "code", "", fb, []string{"ignoring VISUAL=code", "using " + fb, "SEDIT_EDITOR"}},
		{"", "", "cursor", fb, []string{"ignoring EDITOR=cursor"}},
		{"", "code", "nano", "nano", []string{"ignoring VISUAL=code", "using nano"}},
		{"", "code", "subl", fb, []string{"VISUAL=code", "EDITOR=subl"}},
		{"", "/usr/local/bin/code --reuse-window", "", fb, []string{"ignoring VISUAL=/usr/local/bin/code --reuse-window"}},
	} {
		t.Setenv("SEDIT_EDITOR", tc.sedit)
		t.Setenv("VISUAL", tc.visual)
		t.Setenv("EDITOR", tc.editor)
		got, note := chooseEditor()
		if got != tc.want {
			t.Errorf("%+v: editor = %q, want %q", tc, got, tc.want)
		}
		if tc.noteHas == nil && note != "" {
			t.Errorf("%+v: unexpected note %q", tc, note)
		}
		for _, sub := range tc.noteHas {
			if !strings.Contains(note, sub) {
				t.Errorf("%+v: note %q lacks %q", tc, note, sub)
			}
		}
	}
}

func TestFallbackEditor(t *testing.T) {
	if got := fallbackEditor(); got != "vim" && got != "vi" {
		t.Errorf("got %q", got)
	}
}

func TestStatuslineEnabled(t *testing.T) {
	for val, want := range map[string]bool{
		"": true, "1": true, "yes": true, "on": true, "anything": true,
		"0": false, "false": false, "FALSE": false, "no": false, "Off": false, " 0 ": false,
	} {
		t.Setenv("SEDIT_STATUSLINE", val)
		if got := statuslineEnabled(); got != want {
			t.Errorf("SEDIT_STATUSLINE=%q: got %v, want %v", val, got, want)
		}
	}
}

func TestRunReturnsWhatTheEditorWrote(t *testing.T) {
	useEditor(t, script(t, t.TempDir(), "ed.sh", "echo appended >> \"$1\"\n"))
	got, err := Run("notes.md", []byte("original\n"))
	if err != nil || string(got) != "original\nappended\n" {
		t.Fatalf("got %q, %v", got, err)
	}
}

// The editor must see a file named after the real one, not a generic name.
func TestRunNamesTheTempFileAfterTheRealFile(t *testing.T) {
	dir := t.TempDir()
	seen := filepath.Join(dir, "seen")
	useEditor(t, script(t, dir, "ed.sh", "basename \"$1\" > "+seen+"\necho x >> \"$1\"\n"))
	if _, err := Run(filepath.Join(dir, "some", "dir", "notes.md"), nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(seen); strings.TrimSpace(string(b)) != "notes.md" {
		t.Fatalf("editor saw %q", b)
	}
}

func TestRunTempFileIsPrivateAndRemoved(t *testing.T) {
	dir := t.TempDir()
	info := filepath.Join(dir, "info")
	// "ls -ld" prints the same permission string on macOS and Linux (macOS may
	// append "@"), so the first ten characters are portable.
	useEditor(t, script(t, dir, "ed.sh",
		"d=$(dirname \"$1\")\nls -ld \"$1\" | cut -c1-10 > "+info+"\nls -ld \"$d\" | cut -c1-10 >> "+info+"\necho \"$d\" >> "+info+"\necho x >> \"$1\"\n"))
	if _, err := Run("notes.md", []byte("secret\n")); err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(mustRead(t, info))
	if len(lines) != 3 {
		t.Fatalf("unexpected info %q", lines)
	}
	if lines[0] != "-rw-------" || lines[1] != "drwx------" {
		t.Errorf("file %s, directory %s; want -rw------- and drwx------", lines[0], lines[1])
	}
	if _, err := os.Stat(lines[2]); err == nil {
		t.Errorf("the temp directory %s still exists", lines[2])
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunEditorFailureReturnsAnError(t *testing.T) {
	useEditor(t, script(t, t.TempDir(), "ed.sh", "echo partial >> \"$1\"\nexit 3\n"))
	got, err := Run("notes.md", []byte("keep\n"))
	if err == nil || !strings.Contains(err.Error(), "editor failed") || got != nil {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestStatuslineOptOut(t *testing.T) {
	// With SEDIT_STATUSLINE=0 no vim script is written and -S isn't passed. The
	// editor here is a script, so look at the temp directory it was given.
	for _, tc := range []struct {
		env     string
		wantVim bool
	}{{"", true}, {"0", false}} {
		dir := t.TempDir()
		seen := filepath.Join(dir, "seen")
		// Run only writes sedit.vim for vim-family editors' benefit but writes it
		// whenever the banner is enabled, so a listing shows whether it did.
		useEditor(t, script(t, dir, "ed.sh", "ls \"$(dirname \"$1\")\" > "+seen+"\necho x >> \"$1\"\n"))
		t.Setenv("SEDIT_STATUSLINE", tc.env)
		if _, err := Run("notes.md", nil); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(mustRead(t, seen), "sedit.vim"); got != tc.wantVim {
			t.Errorf("SEDIT_STATUSLINE=%q: vim script present = %v, want %v", tc.env, got, tc.wantVim)
		}
	}

	t.Setenv("SEDIT_EDITOR", "vim")
	t.Setenv("SEDIT_STATUSLINE", "0")
	if got := strings.Join(mustCmd("notes.md", ""), " "); strings.Contains(got, "-S") || !strings.Contains(got, "titlestring") {
		t.Errorf("opt-out args: %q", got)
	}
}

// An editor that detaches (like "code" without --wait) must not go unnoticed.
func TestRunFastExitWithoutChangesWarns(t *testing.T) {
	dir := t.TempDir()
	useEditor(t, script(t, dir, "detach.sh", "( sleep 1; echo x >> \"$1\" ) &\nexit 0\n"))
	var edited []byte
	out := capture(t, func() {
		var err error
		if edited, err = Run("notes.md", []byte("same\n")); err != nil {
			t.Fatal(err)
		}
	})
	if string(edited) != "same\n" {
		t.Errorf("edited = %q", edited)
	}
	for _, want := range []string{"warning: the editor exited after", "code --wait"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

// An editor that exits quickly but did change the file is fine: no warning.
func TestRunFastExitWithChangesDoesNotWarn(t *testing.T) {
	useEditor(t, script(t, t.TempDir(), "ed.sh", "echo one >> \"$1\"\n"))
	out := capture(t, func() {
		if _, err := Run("notes.md", nil); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "the editor exited") {
		t.Errorf("unexpected fast-exit warning: %q", out)
	}
}

// End to end: with VISUAL=code sedit must not run "code" at all, but use vim.
// PATH holds only fake editors, so no real editor is started.
func TestRunIgnoresAGraphicalEditorFromVisual(t *testing.T) {
	bin := t.TempDir()
	marks := t.TempDir()
	fake := func(name string) {
		script(t, bin, name, "for last; do :; done\necho "+name+" >> "+filepath.Join(marks, name)+"\necho x >> \"$last\"\n")
	}
	fake("code")
	fake("vim")
	t.Setenv("PATH", bin)
	t.Setenv("EDITOR", "")
	t.Setenv("SEDIT_STATUSLINE", "0")
	ran := func(name string) bool { _, err := os.Stat(filepath.Join(marks, name)); return err == nil }

	// 1. VISUAL=code: ignored, vim is used, and the user is told.
	t.Setenv("VISUAL", "code")
	t.Setenv("SEDIT_EDITOR", "")
	out := capture(t, func() {
		if _, err := Run("a.txt", nil); err != nil {
			t.Fatal(err)
		}
	})
	if ran("code") || !ran("vim") {
		t.Errorf("code ran = %v, vim ran = %v", ran("code"), ran("vim"))
	}
	if !strings.Contains(out, "ignoring VISUAL=code") {
		t.Errorf("no notice in %q", out)
	}

	// 2. SEDIT_EDITOR=code: explicit, so it is used (with --wait).
	os.Remove(filepath.Join(marks, "vim"))
	t.Setenv("SEDIT_EDITOR", "code")
	out = capture(t, func() {
		if _, err := Run("b.txt", nil); err != nil {
			t.Fatal(err)
		}
	})
	if !ran("code") || ran("vim") {
		t.Errorf("code ran = %v, vim ran = %v", ran("code"), ran("vim"))
	}
	if !strings.Contains(out, `using "code --wait"`) || strings.Contains(out, "ignoring") {
		t.Errorf("unexpected output %q", out)
	}
}
