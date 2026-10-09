package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func decryptFile(t *testing.T, path, pw string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decrypt([]byte(pw), data)
	if err != nil {
		t.Fatalf("decrypt with %q: %v", pw, err)
	}
	return string(got)
}

func TestNewFileWithDefaultFlag(t *testing.T) {
	fs := useFakeStore(t)
	fs.m[defaultAccount] = []byte("dpw")
	appendEditor(t, "one")
	path := filepath.Join(t.TempDir(), "f")
	withStdin(t, "") // no typing needed
	if err := edit(path, false, choiceDefault); err != nil {
		t.Fatal(err)
	}
	if got := decryptFile(t, path, "dpw"); got != "one\n" {
		t.Fatalf("got %q", got)
	}
}

func TestNewFileWithCustomFlag(t *testing.T) {
	fs := useFakeStore(t)
	fs.m[defaultAccount] = []byte("dpw")
	appendEditor(t, "one")
	path := filepath.Join(t.TempDir(), "f")
	withStdin(t, "cpw\n")
	if err := edit(path, false, choiceCustom); err != nil {
		t.Fatal(err)
	}
	decryptFile(t, path, "cpw")
}

func TestAskWithoutTerminalUsesCustom(t *testing.T) {
	fs := useFakeStore(t)
	fs.m[defaultAccount] = []byte("dpw")
	appendEditor(t, "one")
	path := filepath.Join(t.TempDir(), "f")
	withStdin(t, "cpw\n") // stdin isn't a terminal
	if err := edit(path, false, choiceAsk); err != nil {
		t.Fatal(err)
	}
	decryptFile(t, path, "cpw") // must not have used the default
}

func TestDefaultFlagWithoutDefault(t *testing.T) {
	useFakeStore(t)
	appendEditor(t, "one")
	path := filepath.Join(t.TempDir(), "f")
	withStdin(t, "")
	err := edit(path, false, choiceDefault)
	if err == nil || !strings.Contains(err.Error(), "no default password") {
		t.Fatalf("got %v", err)
	}
	if _, serr := os.Stat(path); serr == nil {
		t.Fatal("file was created")
	}
}

func TestOpenFallsBackToDefault(t *testing.T) {
	fs := useFakeStore(t)
	fs.m[defaultAccount] = []byte("dpw")
	appendEditor(t, "one")
	path := filepath.Join(t.TempDir(), "f")
	withStdin(t, "")
	if err := edit(path, false, choiceDefault); err != nil {
		t.Fatal(err)
	}
	appendEditor(t, "two")
	withStdin(t, "") // opened with the default, no prompt
	if err := edit(path, false, choiceAsk); err != nil {
		t.Fatal(err)
	}
	if got := decryptFile(t, path, "dpw"); got != "one\ntwo\n" {
		t.Fatalf("got %q", got)
	}
}

func TestSensitiveFileKeepsDefault(t *testing.T) {
	fs := useFakeStore(t)
	fs.m[defaultAccount] = []byte("dpw")
	appendEditor(t, "one")
	path := filepath.Join(t.TempDir(), "f")
	withStdin(t, "cpw\n")
	if err := edit(path, false, choiceCustom); err != nil {
		t.Fatal(err)
	}
	appendEditor(t, "two")
	withStdin(t, "cpw\n") // default fails silently, then we prompt
	if err := edit(path, false, choiceAsk); err != nil {
		t.Fatal(err)
	}
	if string(fs.m[defaultAccount]) != "dpw" {
		t.Fatal("default was removed or changed")
	}
	if got := decryptFile(t, path, "cpw"); got != "one\ntwo\n" {
		t.Fatalf("got %q", got)
	}
}

func TestFileEntryBeatsDefault(t *testing.T) {
	fs := useFakeStore(t)
	appendEditor(t, "one")
	path := filepath.Join(t.TempDir(), "f")
	withStdin(t, "cpw\n")
	if err := edit(path, true, choiceCustom); err != nil {
		t.Fatal(err)
	}
	fs.m[defaultAccount] = []byte("dpw")
	appendEditor(t, "two")
	withStdin(t, "")
	if err := edit(path, false, choiceAsk); err != nil {
		t.Fatal(err)
	}
	decryptFile(t, path, "cpw")
}

func TestPasswdSwitchesToDefault(t *testing.T) {
	fs := useFakeStore(t)
	fs.m[defaultAccount] = []byte("dpw")
	appendEditor(t, "one")
	path := filepath.Join(t.TempDir(), "f")
	withStdin(t, "cpw\n")
	if err := edit(path, false, choiceCustom); err != nil {
		t.Fatal(err)
	}
	withStdin(t, "cpw\n") // current password (default fails first)
	if err := passwd(path, choiceDefault); err != nil {
		t.Fatal(err)
	}
	decryptFile(t, path, "dpw")
}

func TestEncryptWithDefault(t *testing.T) {
	fs := useFakeStore(t)
	fs.m[defaultAccount] = []byte("dpw")
	path := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(path, []byte("plain\n"), 0o600)
	withStdin(t, "")
	if err := encrypt(path, true, choiceDefault); err != nil {
		t.Fatal(err)
	}
	if got := decryptFile(t, path, "dpw"); got != "plain\n" {
		t.Fatalf("got %q", got)
	}
}

func TestSetAndForgetDefault(t *testing.T) {
	fs := useFakeStore(t)
	withStdin(t, "dpw\n")
	if err := setDefault(); err != nil {
		t.Fatal(err)
	}
	if string(fs.m[defaultAccount]) != "dpw" {
		t.Fatalf("got %q", fs.m[defaultAccount])
	}
	if err := forgetDefault(); err != nil {
		t.Fatal(err)
	}
	if err := forgetDefault(); err == nil {
		t.Fatal("expected error when no default")
	}
}

func TestDefaultWithoutStore(t *testing.T) {
	old := store
	store = nil
	defer func() { store = old }()
	if err := setDefault(); err == nil {
		t.Fatal("setDefault should fail")
	}
	if err := forgetDefault(); err == nil {
		t.Fatal("forgetDefault should fail")
	}
	// Opening a file must still work without a store (prompting only).
	appendEditor(t, "one")
	path := filepath.Join(t.TempDir(), "f")
	withStdin(t, "pw\n")
	if err := edit(path, false, choiceAsk); err != nil {
		t.Fatal(err)
	}
}

func TestParseChoice(t *testing.T) {
	for in, want := range map[string]pwChoice{"d": choiceDefault, " Default\n": choiceDefault, "c": choiceCustom, "CUSTOM": choiceCustom} {
		if got, ok := parseChoice(in); !ok || got != want {
			t.Errorf("%q: got %v, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "\n", "x", "dc"} {
		if _, ok := parseChoice(in); ok {
			t.Errorf("%q should be invalid", in)
		}
	}
}

func TestParseArgs(t *testing.T) {
	good := map[string]opts{
		"f":                               {mode: "edit", file: "f"},
		"--default f":                     {mode: "edit", file: "f", choice: choiceDefault},
		"f --custom":                      {mode: "edit", file: "f", choice: choiceCustom},
		"-p f":                            {mode: "print", file: "f"},
		"--passwd --default f":            {mode: "passwd", file: "f", choice: choiceDefault},
		"--encrypt -y f":                  {mode: "encrypt", file: "f", yes: true},
		"--encrypt f -y --custom":         {mode: "encrypt", file: "f", yes: true, choice: choiceCustom},
		"--remember --default f":          {mode: "remember", file: "f", choice: choiceDefault},
		"--forget f":                      {mode: "forget", file: "f"},
		"--set-default":                   {mode: "set-default"},
		"--forget-default":                {mode: "forget-default"},
		"-h":                              {mode: "help"},
		"--help":                          {mode: "help"},
		"f --encrypt --help":              {mode: "help"},
		"--version":                       {mode: "version"},
		"--share f --to k":                {mode: "share", file: "f", to: []string{"k"}},
		"--share f --to k1 --to k2":       {mode: "share", file: "f", to: []string{"k1", "k2"}},
		"--to k --share f":                {mode: "share", file: "f", to: []string{"k"}},
		"--share f -R r.txt -a":           {mode: "share", file: "f", recipFiles: []string{"r.txt"}, armor: true},
		"--share f --to k -o out.age":     {mode: "share", file: "f", to: []string{"k"}, out: "out.age"},
		"--share f --to k -o -":           {mode: "share", file: "f", to: []string{"k"}, out: "-"},
		"--share f --to k --armor":        {mode: "share", file: "f", to: []string{"k"}, armor: true},
		"--import f.age -i k":             {mode: "import", file: "f.age", identities: []string{"k"}},
		"--import f.age":                  {mode: "import", file: "f.age"},
		"--import f.age -i a -i b -o out": {mode: "import", file: "f.age", identities: []string{"a", "b"}, out: "out"},
		"--default --import f.age":        {mode: "import", file: "f.age", choice: choiceDefault},
		"-i k --import f.age":             {mode: "import", file: "f.age", identities: []string{"k"}},
	}
	for in, want := range good {
		got, err := parseArgs(strings.Fields(in))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "-p", "a b", "-x f", "-p --passwd f", "--default --custom f",
		"-y f", "-p --default f", "--set-default f", "--forget-default f", "--forget --custom f",
		"--share f", "--share", "--share f --to", "--share f --to k -o", "--share f --to k -o a -o b",
		"-i k f", "--import f -i", "--import f --to k", "--import f -a", "--import f -y", "--import f -o a -o b",
		"--share f --to k -i k2", "-o x f", "-a f", "-R r f", "--share f --to k --default f2"} {
		if _, err := parseArgs(strings.Fields(in)); err == nil {
			t.Errorf("%q should be rejected", in)
		}
	}
}
