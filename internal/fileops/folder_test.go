package fileops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/eddienko/secure-edit/internal/keystore"
	"github.com/eddienko/secure-edit/internal/vault"
)

func remember(t *testing.T, path, pw string) string {
	t.Helper()
	account, err := keystore.Account(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := keystore.Backend.Set(account, []byte(pw)); err != nil {
		t.Fatal(err)
	}
	return account
}

func TestMkDirMakesAPrivateFolderWithLabels(t *testing.T) {
	root := t.TempDir()
	path, err := MkDir(root, "bank", vault.Meta{Title: "Banking", Description: "cards"})
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Errorf("folder: %v, %v", fi, err)
	}
	if m := vault.ReadFolderMeta(path); m != (vault.Meta{Title: "Banking", Description: "cards"}) {
		t.Errorf("labels %+v", m)
	}
	// Without labels, no label file.
	plain, _ := MkDir(root, "plain", vault.Meta{})
	if _, err := os.Stat(filepath.Join(plain, vault.FolderMetaName)); err == nil {
		t.Error("a label file for no labels")
	}
}

func TestMkDirRefusals(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "taken"), 0o700)
	for name, want := range map[string]string{
		"":        "give it a name",
		"a/b":     "not a path",
		"..":      "not a path",
		".hidden": "hidden",
		"old.bak": "hidden",
		"taken":   "already exists",
	} {
		if _, err := MkDir(root, name, vault.Meta{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", name, err, want)
		}
	}
	if _, err := MkDir(root, "ok", vault.Meta{Title: "a\x1bb"}); err == nil {
		t.Error("bad labels accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "ok")); err == nil {
		t.Error("a folder was left behind after the labels were refused")
	}
	if _, err := MkDir(filepath.Join(root, "missing"), "x", vault.Meta{}); err == nil {
		t.Error("made a folder in one that doesn't exist")
	}
}

// A tree: root/work/{a.txt, a.txt.bak, deep/b.txt, notes.md}, with a remembered
// password for each sedit file.
func folderTree(t *testing.T) (root string, accounts map[string]string) {
	t.Helper()
	root = t.TempDir()
	work := filepath.Join(root, "work")
	os.MkdirAll(filepath.Join(work, "deep"), 0o700)
	a := seditFile(t, work, "a.txt", "a")
	seditFile(t, work, "a.txt.bak", "old a")
	b := seditFile(t, filepath.Join(work, "deep"), "b.txt", "b")
	os.WriteFile(filepath.Join(work, "notes.md"), []byte("plain"), 0o600)
	vault.WriteFolderMeta(work, vault.Meta{Title: "Work"})
	accounts = map[string]string{"a.txt": remember(t, a, "pw-a"), "deep/b.txt": remember(t, b, "pw-b")}
	return root, accounts
}

func TestMoveDirCarriesFilesBackupsLabelsAndPasswords(t *testing.T) {
	st := useStore(t)
	root, old := folderTree(t)
	res, err := MoveDir(filepath.Join(root, "work"), filepath.Join(root, "job"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 2 || res.Passwords != 2 || len(res.Warnings) != 0 {
		t.Errorf("result %+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "work")); err == nil {
		t.Error("the old folder is still there")
	}
	job := filepath.Join(root, "job")
	for _, f := range []string{"a.txt", "a.txt.bak", "deep/b.txt", "notes.md"} {
		if _, err := os.Stat(filepath.Join(job, f)); err != nil {
			t.Errorf("%s did not move: %v", f, err)
		}
	}
	if m := vault.ReadFolderMeta(job); m.Title != "Work" {
		t.Errorf("the folder's labels did not move: %+v", m)
	}
	for rel, oldAccount := range old {
		if _, ok := st.m[oldAccount]; ok {
			t.Errorf("the old entry for %s is still stored", rel)
		}
		account, _ := keystore.Account(filepath.Join(job, rel))
		if got := string(st.m[account]); !strings.HasPrefix(got, "pw-") {
			t.Errorf("no password stored for the new %s (%q)", rel, got)
		}
	}
	// And they still open the files.
	account, _ := keystore.Account(filepath.Join(job, "deep", "b.txt"))
	data, _ := os.ReadFile(filepath.Join(job, "deep", "b.txt"))
	if _, err := vault.Decrypt([]byte("pw"), data); err != nil {
		t.Fatal(err)
	}
	if string(st.m[account]) != "pw-b" {
		t.Errorf("stored password %q", st.m[account])
	}
}

func TestMoveDirIntoAnotherFolder(t *testing.T) {
	useStore(t)
	root, _ := folderTree(t)
	os.Mkdir(filepath.Join(root, "archive"), 0o700)
	if _, err := MoveDir(filepath.Join(root, "work"), filepath.Join(root, "archive", "work")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "archive", "work", "deep", "b.txt")); err != nil {
		t.Error(err)
	}
}

func TestMoveDirRefusals(t *testing.T) {
	useStore(t)
	root, _ := folderTree(t)
	work := filepath.Join(root, "work")
	os.Mkdir(filepath.Join(root, "other"), 0o700)
	os.Symlink(work, filepath.Join(root, "link"))
	file := seditFile(t, root, "f.txt", "x")

	for name, c := range map[string]struct {
		from, to, want string
	}{
		"a symlink":     {filepath.Join(root, "link"), filepath.Join(root, "x"), "symlink"},
		"a file":        {file, filepath.Join(root, "x"), "not a folder"},
		"missing":       {filepath.Join(root, "nope"), filepath.Join(root, "x"), "no such file"},
		"onto a folder": {work, filepath.Join(root, "other"), "refusing to overwrite"},
		"onto a file":   {work, file, "refusing to overwrite"},
		"into itself":   {work, filepath.Join(work, "deep", "work"), "into itself"},
		"onto itself":   {work, work, "into itself"},
		"no parent":     {work, filepath.Join(root, "missing", "x"), "no such folder"},
	} {
		_, err := MoveDir(c.from, c.to)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	if _, err := os.Stat(filepath.Join(work, "a.txt")); err != nil {
		t.Errorf("a refusal changed the folder: %v", err)
	}
}

func TestMoveDirChangesNothingIfAFileIsBeingEdited(t *testing.T) {
	st := useStore(t)
	root, old := folderTree(t)
	work := filepath.Join(root, "work")
	release, err := vault.Lock(filepath.Join(work, "deep", "b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, err = MoveDir(work, filepath.Join(root, "job"))
	var locked *vault.ErrLocked
	if !errors.As(err, &locked) {
		t.Fatalf("got %v, want a lock error", err)
	}
	if _, err := os.Stat(work); err != nil {
		t.Error("the folder moved though a file was locked")
	}
	for rel, account := range old {
		if _, ok := st.m[account]; !ok {
			t.Errorf("the stored password of %s was touched", rel)
		}
	}
}

func TestMoveDirKeepsOldEntriesWhenTheNewOnesCantBeStored(t *testing.T) {
	st := useStore(t)
	root, old := folderTree(t)
	st.setErr = errors.New("keychain is locked")
	res, err := MoveDir(filepath.Join(root, "work"), filepath.Join(root, "job"))
	if err != nil {
		t.Fatalf("the folder move should still succeed: %v", err)
	}
	if len(res.Warnings) != 2 || !strings.Contains(res.Warnings[0], "keychain is locked") {
		t.Errorf("warnings %v", res.Warnings)
	}
	for rel, account := range old {
		if _, ok := st.m[account]; !ok {
			t.Errorf("the old entry for %s was removed though the new one failed", rel)
		}
	}
	if res.Passwords != 0 {
		t.Errorf("counted %d moved passwords", res.Passwords)
	}
}

func TestMoveDirOnAnotherFileSystem(t *testing.T) {
	useStore(t)
	root, _ := folderTree(t)
	old := rename
	rename = func(string, string) error { return &os.LinkError{Op: "rename", Err: syscall.EXDEV} }
	defer func() { rename = old }()
	_, err := MoveDir(filepath.Join(root, "work"), filepath.Join(root, "job"))
	if err == nil || !strings.Contains(err.Error(), "another file system") {
		t.Errorf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "work")); err != nil {
		t.Error("the folder is gone")
	}
}

func TestMoveDirWorksWithoutAKeychain(t *testing.T) {
	old := keystore.Backend
	keystore.Backend = nil
	defer func() { keystore.Backend = old }()
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "w"), 0o700)
	seditFile(t, filepath.Join(root, "w"), "a.txt", "x")
	if res, err := MoveDir(filepath.Join(root, "w"), filepath.Join(root, "v")); err != nil || res.Files != 1 {
		t.Errorf("%+v, %v", res, err)
	}
}

func TestRemoveEmptyDir(t *testing.T) {
	root := t.TempDir()
	// Empty, or holding only what goes with a folder.
	only := filepath.Join(root, "only")
	os.Mkdir(only, 0o700)
	vault.WriteFolderMeta(only, vault.Meta{Title: "t"})
	os.WriteFile(filepath.Join(only, ".DS_Store"), []byte("finder"), 0o600)
	os.WriteFile(filepath.Join(only, ".gone.txt.lock"), nil, 0o600)
	if err := RemoveEmptyDir(only); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(only); err == nil {
		t.Error("the folder is still there")
	}
	nothing := filepath.Join(root, "nothing")
	os.Mkdir(nothing, 0o700)
	if err := RemoveEmptyDir(nothing); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveEmptyDirRefusesAnythingElse(t *testing.T) {
	root := t.TempDir()
	for name, setup := range map[string]func(dir string){
		"a sedit file":    func(d string) { seditFile(t, d, "a.txt", "x") },
		"a plain file":    func(d string) { os.WriteFile(filepath.Join(d, "readme.md"), nil, 0o600) },
		"a backup":        func(d string) { seditFile(t, d, "a.txt.bak", "x") },
		"a folder":        func(d string) { os.Mkdir(filepath.Join(d, "sub"), 0o700) },
		"a dot file":      func(d string) { os.WriteFile(filepath.Join(d, ".git"), nil, 0o600) },
		"a held lock":     func(d string) { os.WriteFile(filepath.Join(d, ".a.lock"), []byte("123\n"), 0o600) },
		"a symlink label": func(d string) { os.Symlink("/etc/hosts", filepath.Join(d, vault.FolderMetaName)) },
	} {
		dir := filepath.Join(root, strings.ReplaceAll(name, " ", "-"))
		os.Mkdir(dir, 0o700)
		setup(dir)
		err := RemoveEmptyDir(dir)
		var ne *NotEmptyError
		if !errors.As(err, &ne) {
			t.Errorf("%s: got %v", name, err)
			continue
		}
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s: the folder was removed", name)
		}
	}
	// What is said: names, folders marked, a count after a few.
	big := filepath.Join(root, "big")
	os.Mkdir(big, 0o700)
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		os.WriteFile(filepath.Join(big, n+".txt"), nil, 0o600)
	}
	os.Mkdir(filepath.Join(big, "zsub"), 0o700)
	err := RemoveEmptyDir(big)
	if err == nil || !strings.Contains(err.Error(), "a.txt, b.txt, c.txt, d.txt and 3 more") {
		t.Errorf("message %v", err)
	}
}

func TestRemoveEmptyDirKeepsLabelsWhenRefused(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "d")
	os.Mkdir(dir, 0o700)
	vault.WriteFolderMeta(dir, vault.Meta{Title: "keep"})
	seditFile(t, dir, "a.txt", "x")
	RemoveEmptyDir(dir)
	if m := vault.ReadFolderMeta(dir); m.Title != "keep" {
		t.Errorf("a refused delete lost the title: %+v", m)
	}
}

func TestRemoveEmptyDirRefusesASymlinkAndAFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "t")
	os.Mkdir(target, 0o700)
	os.Symlink(target, filepath.Join(root, "link"))
	if err := RemoveEmptyDir(filepath.Join(root, "link")); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("symlink: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Error("the target was removed")
	}
	f := seditFile(t, root, "f.txt", "x")
	if err := RemoveEmptyDir(f); err == nil || !strings.Contains(err.Error(), "not a folder") {
		t.Errorf("file: %v", err)
	}
}
