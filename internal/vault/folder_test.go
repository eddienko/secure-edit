package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFolderMetaRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if m := ReadFolderMeta(dir); !m.IsZero() {
		t.Fatalf("a folder without the file has %+v", m)
	}
	want := Meta{Title: "Banking", Description: "accounts\nand cards"}
	if err := WriteFolderMeta(dir, want); err != nil {
		t.Fatal(err)
	}
	if got := ReadFolderMeta(dir); got != want {
		t.Errorf("got %+v", got)
	}
	if fi, err := os.Stat(filepath.Join(dir, FolderMetaName)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v, %v", fi, err)
	}
	// Writing none removes the file.
	if err := WriteFolderMeta(dir, Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, FolderMetaName)); err == nil {
		t.Error("the file stayed")
	}
	if err := WriteFolderMeta(dir, Meta{}); err != nil {
		t.Errorf("removing what isn't there: %v", err)
	}
}

func TestFolderMetaIsValidatedAndCleaned(t *testing.T) {
	dir := t.TempDir()
	if err := WriteFolderMeta(dir, Meta{Title: "a\x1bb"}); err == nil {
		t.Error("a control character was accepted")
	}
	if err := WriteFolderMeta(dir, Meta{Title: strings.Repeat("x", MaxTitle+1)}); err == nil {
		t.Error("a long title was accepted")
	}
	if err := WriteFolderMeta(dir, Meta{Title: "  Spaced  "}); err != nil {
		t.Fatal(err)
	}
	if got := ReadFolderMeta(dir); got.Title != "Spaced" {
		t.Errorf("title %q", got.Title)
	}
}

// Whatever is in the file, reading it gives labels or nothing: never an error,
// never something that wouldn't pass the limits.
func TestFolderMetaReadsOnlyWhatIsValid(t *testing.T) {
	for name, content := range map[string]string{
		"not json":       "hello",
		"wrong version":  `{"version":2,"title":"x"}`,
		"no version":     `{"title":"x"}`,
		"control char":   `{"version":1,"title":"a\u001bb"}`,
		"title too long": `{"version":1,"title":"` + strings.Repeat("x", MaxTitle+1) + `"}`,
		"huge file":      `{"version":1,"title":"x","description":"` + strings.Repeat("d", maxFolderMetaFile) + `"}`,
		"wrong type":     `{"version":1,"title":5}`,
		"empty":          ``,
	} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, FolderMetaName), []byte(content), 0o600)
		if m := ReadFolderMeta(dir); !m.IsZero() {
			t.Errorf("%s: read %+v", name, m)
		}
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, FolderMetaName), []byte(`{"version":1,"title":"ok","extra":true}`), 0o600)
	if m := ReadFolderMeta(dir); m.Title != "ok" {
		t.Errorf("unknown fields should be ignored: %+v", m)
	}
}

func TestFolderMetaIsNeverReadOrWrittenThroughASymlink(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "elsewhere")
	os.WriteFile(secret, []byte(`{"version":1,"title":"from elsewhere"}`), 0o600)
	os.Symlink(secret, filepath.Join(dir, FolderMetaName))
	if m := ReadFolderMeta(dir); !m.IsZero() {
		t.Errorf("read through a symlink: %+v", m)
	}
	if err := WriteFolderMeta(dir, Meta{Title: "mine"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(secret); !strings.Contains(string(b), "from elsewhere") {
		t.Errorf("wrote through the symlink: %s", b)
	}
	if got := ReadFolderMeta(dir); got.Title != "mine" {
		t.Errorf("after writing: %+v", got)
	}
}
