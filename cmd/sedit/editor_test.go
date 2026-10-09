package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr runs f and returns what it wrote to os.Stderr.
func captureStderr(t *testing.T, f func()) string {
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
	if got := strings.Join(editorCmd("notes.md"), " "); !strings.Contains(got, "titlestring") || !strings.Contains(got, "noswapfile") {
		t.Errorf("vim: %q", got)
	}
	t.Setenv("EDITOR", "nano -w")
	if got := editorCmd("notes.md"); len(got) != 2 || got[0] != "nano" {
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
		args := append([]string{"-es", "-u", "NONE"}, vimArgs(name)...)
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

// The editor must see a file named after the real one, not a generic name.
func TestEditorSeesRealFileName(t *testing.T) {
	useFakeStore(t)
	dir := t.TempDir()
	seen := filepath.Join(dir, "seen")
	script := filepath.Join(dir, "ed.sh")
	os.WriteFile(script, []byte("#!/bin/sh\nbasename \"$1\" > "+seen+"\necho x >> \"$1\"\n"), 0o755)
	t.Setenv("EDITOR", script)
	t.Setenv("VISUAL", "")

	withStdin(t, "pw\n")
	if err := edit(filepath.Join(dir, "notes.md"), false, choiceAsk); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(seen); strings.TrimSpace(string(b)) != "notes.md" {
		t.Fatalf("editor saw %q", b)
	}
}

func TestMessages(t *testing.T) {
	fs := useFakeStore(t)
	appendEditor(t, "one")
	path := filepath.Join(t.TempDir(), "secrets")

	out := captureStderr(t, func() {
		withStdin(t, "pw\n")
		if err := edit(path, true, choiceAsk); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"created " + path + " (encrypted)", "password remembered in Keychain"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}

	appendEditor(t, "two")
	out = captureStderr(t, func() {
		withStdin(t, "")
		if err := edit(path, false, choiceAsk); err != nil {
			t.Fatal(err)
		}
	})
	if want := "saved " + path + " (encrypted; password from Keychain)"; !strings.Contains(out, want) {
		t.Errorf("missing %q in %q", want, out)
	}

	// no changes
	script := filepath.Join(t.TempDir(), "noop.sh")
	os.WriteFile(script, []byte("#!/bin/sh\ntrue\n"), 0o755)
	t.Setenv("EDITOR", script)
	out = captureStderr(t, func() {
		withStdin(t, "")
		if err := edit(path, false, choiceAsk); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "no changes to "+path) {
		t.Errorf("got %q", out)
	}

	// default password
	delete(fs.m, mustAccount(t, path))
	fs.m[defaultAccount] = []byte("dpw")
	d := filepath.Join(t.TempDir(), "d")
	appendEditor(t, "one")
	withStdin(t, "")
	if err := edit(d, false, choiceDefault); err != nil {
		t.Fatal(err)
	}
	appendEditor(t, "two")
	out = captureStderr(t, func() {
		withStdin(t, "")
		if err := edit(d, false, choiceAsk); err != nil {
			t.Fatal(err)
		}
	})
	if want := "(encrypted; default password from Keychain)"; !strings.Contains(out, want) {
		t.Errorf("missing %q in %q", want, out)
	}

	// passwd and encrypt
	out = captureStderr(t, func() {
		withStdin(t, "newpw\n")
		if err := passwd(d, choiceCustom); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "password changed for "+d) {
		t.Errorf("got %q", out)
	}
	plain := filepath.Join(t.TempDir(), "plain.txt")
	os.WriteFile(plain, []byte("hello"), 0o600)
	out = captureStderr(t, func() {
		withStdin(t, "pw\n")
		if err := encrypt(plain, true, choiceCustom); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "encrypted "+plain) {
		t.Errorf("got %q", out)
	}
}

func mustAccount(t *testing.T, path string) string {
	t.Helper()
	a, err := account(path)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
