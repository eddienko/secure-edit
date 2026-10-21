package fileops

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/eddienko/secure-edit/internal/keystore"
	"github.com/eddienko/secure-edit/internal/vault"
)

type fakeStore struct {
	m      map[string][]byte
	getErr error
	setErr error
}

func (f *fakeStore) Get(a string) ([]byte, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if v, ok := f.m[a]; ok {
		return append([]byte(nil), v...), nil
	}
	return nil, keystore.ErrNotFound
}
func (f *fakeStore) Set(a string, p []byte) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.m[a] = append([]byte(nil), p...)
	return nil
}
func (f *fakeStore) Delete(a string) error {
	if _, ok := f.m[a]; !ok {
		return keystore.ErrNotFound
	}
	delete(f.m, a)
	return nil
}

func useStore(t *testing.T) *fakeStore {
	t.Helper()
	f := &fakeStore{m: map[string][]byte{}}
	old := keystore.Backend
	keystore.Backend = f
	t.Cleanup(func() { keystore.Backend = old })
	return f
}

// seditFile writes a real (cheaply encrypted) sedit file and returns its path.
func seditFile(t *testing.T, dir, name, text string) string {
	t.Helper()
	data, err := vault.Encrypt([]byte("pw"), []byte(text), vault.NewKDFParams(1, 64, 1))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func account(t *testing.T, path string) string {
	t.Helper()
	a, err := keystore.Account(path)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func read(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func gone(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Lstat(p); err == nil {
		t.Errorf("%s still exists", p)
	}
}

func TestMoveRenamesTheFile(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	old := seditFile(t, dir, "a.txt", "hello")
	os.Chmod(old, 0o640)
	before := read(t, old)

	dest, res, err := Move(old, filepath.Join(dir, "b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if dest != filepath.Join(dir, "b.txt") || res.Backup || res.Entry || len(res.Warnings) != 0 {
		t.Errorf("dest %q, result %+v", dest, res)
	}
	gone(t, old)
	if !bytes.Equal(read(t, dest), before) {
		t.Error("contents changed")
	}
	if fi, _ := os.Stat(dest); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v, want 0640", fi.Mode().Perm())
	}
}

func TestMoveIntoADirectory(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	old := seditFile(t, dir, "a.txt", "hello")
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o700)
	dest, _, err := Move(old, sub)
	if err != nil {
		t.Fatal(err)
	}
	if dest != filepath.Join(sub, "a.txt") {
		t.Errorf("dest %q", dest)
	}
	gone(t, old)
	read(t, dest)
}

func TestMoveTakesTheBackupAlong(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	old := seditFile(t, dir, "a.txt", "current")
	bak := seditFile(t, dir, "a.txt.bak", "previous")
	bakBefore := read(t, bak)

	dest, res, err := Move(old, filepath.Join(dir, "b.txt"))
	if err != nil || !res.Backup {
		t.Fatalf("%v %+v", err, res)
	}
	gone(t, bak)
	if !bytes.Equal(read(t, dest+".bak"), bakBefore) {
		t.Error("backup changed")
	}
}

func TestMoveMovesTheStoredPassword(t *testing.T) {
	f := useStore(t)
	dir := t.TempDir()
	old := seditFile(t, dir, "a.txt", "x")
	f.m[account(t, old)] = []byte("secret")
	f.m[keystore.DefaultAccount] = []byte("default")
	f.m["/some/other/file"] = []byte("other")

	dest, res, err := Move(old, filepath.Join(dir, "b.txt"))
	if err != nil || !res.Entry {
		t.Fatalf("%v %+v", err, res)
	}
	if string(f.m[account(t, dest)]) != "secret" {
		t.Errorf("the new entry holds %q", f.m[account(t, dest)])
	}
	if _, ok := f.m[account(t, filepath.Join(dir, "a.txt"))]; ok {
		t.Error("the old entry is still there")
	}
	// Nothing else was touched.
	if string(f.m[keystore.DefaultAccount]) != "default" || string(f.m["/some/other/file"]) != "other" {
		t.Error("another entry changed")
	}
}

func TestMoveWithoutAKeychain(t *testing.T) {
	old := keystore.Backend
	keystore.Backend = nil
	t.Cleanup(func() { keystore.Backend = old })
	dir := t.TempDir()
	src := seditFile(t, dir, "a.txt", "x")
	if _, res, err := Move(src, filepath.Join(dir, "b.txt")); err != nil || res.Entry {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestMoveKeepsTheOldEntryIfTheNewOneCannotBeStored(t *testing.T) {
	f := useStore(t)
	dir := t.TempDir()
	src := seditFile(t, dir, "a.txt", "x")
	acct := account(t, src)
	f.m[acct] = []byte("secret")
	f.setErr = errors.New("keychain is locked")

	dest, res, err := Move(src, filepath.Join(dir, "b.txt"))
	if err != nil {
		t.Fatal(err) // the file itself moved
	}
	read(t, dest)
	if res.Entry || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "old entry was kept") {
		t.Errorf("result %+v", res)
	}
	if string(f.m[acct]) != "secret" {
		t.Error("the old entry was lost")
	}
}

func TestMoveReportsAnUnreadableKeychain(t *testing.T) {
	f := useStore(t)
	f.getErr = errors.New("keychain is locked")
	dir := t.TempDir()
	src := seditFile(t, dir, "a.txt", "x")
	dest, res, err := Move(src, filepath.Join(dir, "b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	read(t, dest)
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "can't read the stored password") {
		t.Errorf("result %+v", res)
	}
}

func TestMoveNeverOverwrites(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	src := seditFile(t, dir, "a.txt", "mine")
	srcBefore := read(t, src)
	existing := seditFile(t, dir, "taken.txt", "theirs")
	existingBefore := read(t, existing)
	link := filepath.Join(dir, "link")
	os.Symlink(existing, link)
	dangling := filepath.Join(dir, "dangling")
	os.Symlink(filepath.Join(dir, "nowhere"), dangling)

	for name, dest := range map[string]string{"file": existing, "symlink": link, "dangling symlink": dangling} {
		_, _, err := Move(src, dest)
		if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if !bytes.Equal(read(t, src), srcBefore) || !bytes.Equal(read(t, existing), existingBefore) {
		t.Error("a file was changed")
	}
	gone(t, filepath.Join(dir, "nowhere"))

	// A leftover backup at the destination is protected too.
	seditFile(t, dir, "a.txt.bak", "previous")
	stale := seditFile(t, dir, "b.txt.bak", "stale backup")
	_, _, err := Move(src, filepath.Join(dir, "b.txt"))
	if err == nil || !strings.Contains(err.Error(), "backup") {
		t.Errorf("existing backup: got %v", err)
	}
	read(t, stale)
	gone(t, filepath.Join(dir, "b.txt"))
}

func TestMoveRefusesWhatIsNotAPlainSeditFile(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.txt")
	os.WriteFile(plain, []byte("not encrypted"), 0o600)
	real := seditFile(t, dir, "real.txt", "x")
	link := filepath.Join(dir, "link")
	os.Symlink(real, link)
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o700)

	for name, tc := range map[string]struct{ path, want string }{
		"plain file": {plain, "not a sedit file"},
		"symlink":    {link, "symlink"},
		"directory":  {sub, "directory"},
	} {
		_, _, err := Move(tc.path, filepath.Join(dir, "elsewhere"))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if _, _, err := Move(filepath.Join(dir, "missing"), filepath.Join(dir, "x")); !os.IsNotExist(err) {
		t.Errorf("missing: got %v", err)
	}
	gone(t, filepath.Join(dir, "elsewhere"))
	read(t, plain)
	read(t, real)
}

func TestMoveToAMissingDirectory(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	src := seditFile(t, dir, "a.txt", "x")
	_, _, err := Move(src, filepath.Join(dir, "no", "such", "dir", "b.txt"))
	if err == nil || !strings.Contains(err.Error(), "no such directory") {
		t.Fatalf("got %v", err)
	}
	read(t, src)
}

func TestMoveToItselfIsRefused(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	src := seditFile(t, dir, "a.txt", "x")
	if _, _, err := Move(src, src); err == nil {
		t.Fatal("expected an error")
	}
	read(t, src)
}

// On a case-insensitive file system "a.txt" and "A.txt" are the same file, and
// renaming one to the other is legitimate. On others it is just a rename.
func TestMoveCaseOnlyRename(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	src := seditFile(t, dir, "a.txt", "x")
	if _, _, err := Move(src, filepath.Join(dir, "A.txt")); err != nil {
		t.Fatal(err)
	}
	var names []string
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != "A.txt" {
		t.Errorf("directory holds %v, want just A.txt", names)
	}
}

func TestMoveRespectsTheLock(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	src := seditFile(t, dir, "a.txt", "x")
	release, err := vault.Lock(src)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, _, err = Move(src, filepath.Join(dir, "b.txt"))
	var locked *vault.ErrLocked
	if !errors.As(err, &locked) {
		t.Fatalf("got %v, want ErrLocked", err)
	}
	read(t, src)
	gone(t, filepath.Join(dir, "b.txt"))
}

func TestMoveLeavesNoLockFileBehind(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	src := seditFile(t, dir, "a.txt", "x")
	if _, _, err := Move(src, filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	gone(t, vault.LockPath(src))
}

// Across file systems rename fails with EXDEV, and the file is copied instead.
func TestMoveAcrossFileSystems(t *testing.T) {
	f := useStore(t)
	dir := t.TempDir()
	src := seditFile(t, dir, "a.txt", "current")
	os.Chmod(src, 0o640)
	seditFile(t, dir, "a.txt.bak", "previous")
	f.m[account(t, src)] = []byte("secret")
	before, bakBefore := read(t, src), read(t, src+".bak")

	old := rename
	rename = func(from, to string) error {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.EXDEV}
	}
	t.Cleanup(func() { rename = old })

	dest, res, err := Move(src, filepath.Join(dir, "b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Backup || !res.Entry {
		t.Errorf("result %+v", res)
	}
	if !bytes.Equal(read(t, dest), before) || !bytes.Equal(read(t, dest+".bak"), bakBefore) {
		t.Error("contents changed")
	}
	if fi, _ := os.Stat(dest); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v, want 0640", fi.Mode().Perm())
	}
	gone(t, src)
	gone(t, src+".bak")
	if string(f.m[account(t, dest)]) != "secret" {
		t.Error("stored password not moved")
	}
}

func TestRemoveDeletesEverythingThatBelongsToTheFile(t *testing.T) {
	f := useStore(t)
	dir := t.TempDir()
	src := seditFile(t, dir, "a.txt", "x")
	seditFile(t, dir, "a.txt.bak", "previous")
	f.m[account(t, src)] = []byte("secret")
	f.m[keystore.DefaultAccount] = []byte("default")
	f.m["/some/other/file"] = []byte("other")
	other := seditFile(t, dir, "keep.txt", "keep me")
	seditFile(t, dir, "keep.txt.bak", "and me")

	res, err := Remove(src)
	if err != nil || !res.Backup || !res.Entry || len(res.Warnings) != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	gone(t, src)
	gone(t, src+".bak")
	gone(t, vault.LockPath(src))
	if len(f.m) != 2 || string(f.m[keystore.DefaultAccount]) != "default" || string(f.m["/some/other/file"]) != "other" {
		t.Errorf("entries now: %v", f.m)
	}
	read(t, other)
	read(t, other+".bak")
}

func TestRemoveWithNoBackupOrEntry(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	src := seditFile(t, dir, "a.txt", "x")
	res, err := Remove(src)
	if err != nil || res.Backup || res.Entry {
		t.Fatalf("%v %+v", err, res)
	}
	gone(t, src)
}

func TestRemoveWithoutAKeychain(t *testing.T) {
	old := keystore.Backend
	keystore.Backend = nil
	t.Cleanup(func() { keystore.Backend = old })
	src := seditFile(t, t.TempDir(), "a.txt", "x")
	if _, err := Remove(src); err != nil {
		t.Fatal(err)
	}
	gone(t, src)
}

func TestRemoveRefusesWhatIsNotAPlainSeditFile(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.txt")
	os.WriteFile(plain, []byte("not encrypted"), 0o600)
	real := seditFile(t, dir, "real.txt", "x")
	link := filepath.Join(dir, "link")
	os.Symlink(real, link)
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o700)

	for name, tc := range map[string]struct{ path, want string }{
		"plain file": {plain, "not a sedit file"},
		"symlink":    {link, "symlink"},
		"directory":  {sub, "directory"},
	} {
		if _, err := Remove(tc.path); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if _, err := Remove(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Errorf("missing: got %v", err)
	}
	read(t, plain)
	read(t, real)
	if _, err := os.Lstat(link); err != nil {
		t.Error("the symlink was removed")
	}
}

func TestRemoveRespectsTheLock(t *testing.T) {
	f := useStore(t)
	dir := t.TempDir()
	src := seditFile(t, dir, "a.txt", "x")
	f.m[account(t, src)] = []byte("secret")
	release, _ := vault.Lock(src)
	defer release()
	_, err := Remove(src)
	var locked *vault.ErrLocked
	if !errors.As(err, &locked) {
		t.Fatalf("got %v, want ErrLocked", err)
	}
	read(t, src)
	if _, ok := f.m[account(t, src)]; !ok {
		t.Error("the stored password was removed although the file was kept")
	}
}

func TestRemoveStillRemovesWhenTheKeychainMisbehaves(t *testing.T) {
	f := useStore(t)
	f.getErr = errors.New("keychain is locked")
	src := seditFile(t, t.TempDir(), "a.txt", "x")
	res, err := Remove(src)
	if err != nil {
		t.Fatal(err)
	}
	gone(t, src)
	if len(res.Warnings) != 1 {
		t.Errorf("result %+v", res)
	}
}

func TestRekeyChangesThePasswordWithoutABackup(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	path := seditFile(t, dir, "a.txt", "hello") // password "pw"

	res, err := Rekey(path, []byte("hello"), []byte("newpw"), false)
	if err != nil || len(res.Warnings) != 0 || res.Entry {
		t.Fatalf("%v %+v", err, res)
	}
	data := read(t, path)
	if got, err := vault.Decrypt([]byte("newpw"), data); err != nil || string(got) != "hello" {
		t.Fatalf("new password: %q %v", got, err)
	}
	if _, err := vault.Decrypt([]byte("pw"), data); err == nil {
		t.Error("the old password still works")
	}
	gone(t, vault.BackupPath(path))
}

func TestRekeyWarnsAboutAnOldBackup(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	path := seditFile(t, dir, "a.txt", "hello")
	seditFile(t, dir, "a.txt.bak", "previous")
	res, err := Rekey(path, []byte("hello"), []byte("newpw"), false)
	if err != nil || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "still encrypted with the old password") {
		t.Fatalf("%v %+v", err, res)
	}
	read(t, path+".bak") // left as it was
}

func TestRekeyUpdatesAStoredPassword(t *testing.T) {
	f := useStore(t)
	path := seditFile(t, t.TempDir(), "a.txt", "hello")
	f.m[account(t, path)] = []byte("pw")

	res, err := Rekey(path, []byte("hello"), []byte("newpw"), true)
	if err != nil || !res.Entry {
		t.Fatalf("%v %+v", err, res)
	}
	if string(f.m[account(t, path)]) != "newpw" {
		t.Errorf("stored %q", f.m[account(t, path)])
	}

	// Not asked to update: the entry is left alone.
	f.m[account(t, path)] = []byte("untouched")
	if _, err := Rekey(path, []byte("hello"), []byte("third"), false); err != nil {
		t.Fatal(err)
	}
	if string(f.m[account(t, path)]) != "untouched" {
		t.Error("the entry was changed although updateStored was false")
	}
}

// The file is re-keyed even if the Keychain can't be updated; the error says so.
func TestRekeyReportsAStoreFailureButRekeys(t *testing.T) {
	f := useStore(t)
	f.setErr = errors.New("keychain is locked")
	path := seditFile(t, t.TempDir(), "a.txt", "hello")
	_, err := Rekey(path, []byte("hello"), []byte("newpw"), true)
	var se *StoreUpdateError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "sedit --forget") {
		t.Fatalf("got %v", err)
	}
	if got, err := vault.Decrypt([]byte("newpw"), read(t, path)); err != nil || string(got) != "hello" {
		t.Errorf("the file was not re-keyed: %q %v", got, err)
	}
}

func TestRekeyKeepsTheLabelsAndSetMetaChangesThem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	m := vault.Meta{Title: "Router", Description: "admin page"}
	if err := vault.SaveMeta(path, []byte("old"), []byte("text"), nil, m); err != nil {
		t.Fatal(err)
	}
	if _, err := Rekey(path, []byte("text"), []byte("new"), false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if got, _ := vault.ReadMeta(data); got != m {
		t.Errorf("labels after Rekey: %+v", got)
	}
	if pt, err := vault.Decrypt([]byte("new"), data); err != nil || string(pt) != "text" {
		t.Errorf("Rekey: %q, %v", pt, err)
	}

	m2 := vault.Meta{Title: "Router 2"}
	if err := SetMeta(path, []byte("text"), []byte("new"), m2); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if got, _ := vault.ReadMeta(data); got != m2 {
		t.Errorf("labels after SetMeta: %+v", got)
	}
	if pt, err := vault.Decrypt([]byte("new"), data); err != nil || string(pt) != "text" {
		t.Errorf("SetMeta changed the text: %q, %v", pt, err)
	}
	if _, err := os.Stat(vault.BackupPath(path)); err == nil {
		t.Error("SetMeta made a backup")
	}
	if err := SetMeta(path, []byte("text"), []byte("new"), vault.Meta{Title: "a\nb"}); err == nil {
		t.Error("SetMeta accepted a title with a newline")
	}
}
