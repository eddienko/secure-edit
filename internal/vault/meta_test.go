package vault

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMetaRoundTripsAndNeedsNoPassword(t *testing.T) {
	m := Meta{Title: "VPN login", Description: "work VPN\nrotate every 90 days"}
	enc, err := EncryptMeta([]byte("pw"), []byte("secret"), testKDF, m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadMeta(enc)
	if err != nil || got != m {
		t.Fatalf("ReadMeta = %+v, %v", got, err)
	}
	pt, err := Decrypt([]byte("pw"), enc)
	if err != nil || string(pt) != "secret" {
		t.Fatalf("Decrypt = %q, %v", pt, err)
	}
	if _, err := Decrypt([]byte("nope"), enc); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	if bytes.Contains(enc, []byte("secret")) {
		t.Error("plaintext in the file")
	}
}

func TestNoMetaStaysVersionOne(t *testing.T) {
	enc, err := EncryptMeta([]byte("pw"), []byte("x"), testKDF, Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if enc[6] != 1 {
		t.Errorf("version %d, want 1: files without labels must stay readable by older sedit", enc[6])
	}
	if m, err := ReadMeta(enc); err != nil || !m.IsZero() {
		t.Errorf("ReadMeta = %+v, %v", m, err)
	}
	enc2, _ := EncryptMeta([]byte("pw"), []byte("x"), testKDF, Meta{Title: "t"})
	if enc2[6] != 2 {
		t.Errorf("version %d, want 2", enc2[6])
	}
}

// The labels are in the clear, so a change to them must be caught when the file
// is opened.
func TestMetaTamperDetected(t *testing.T) {
	enc, _ := EncryptMeta([]byte("pw"), []byte("x"), testKDF, Meta{Title: "Bank", Description: "mine"})
	i := bytes.Index(enc, []byte("Bank"))
	if i < 0 {
		t.Fatal("title not found in the header")
	}
	bad := bytes.Clone(enc)
	copy(bad[i:], "Evil")
	if m, _ := ReadMeta(bad); m.Title != "Evil" {
		t.Fatal("test setup: the title did not change")
	}
	if _, err := Decrypt([]byte("pw"), bad); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("a changed title was accepted: %v", err)
	}
}

func TestMetaLimitsAndBadHeaders(t *testing.T) {
	long := strings.Repeat("a", MaxTitle+1)
	if _, err := EncryptMeta([]byte("pw"), nil, testKDF, Meta{Title: long}); err == nil {
		t.Error("an over-long title was accepted")
	}
	if _, err := EncryptMeta([]byte("pw"), nil, testKDF, Meta{Description: strings.Repeat("a", MaxDescription+1)}); err == nil {
		t.Error("an over-long description was accepted")
	}
	for name, m := range map[string]Meta{
		"newline in title":   {Title: "a\nb"},
		"escape in title":    {Title: "a\x1b[31mb"},
		"escape in desc":     {Description: "a\x1b]0;x\x07"},
		"invalid utf-8":      {Title: "a\xffb"},
		"tab in description": {Description: "a\tb"},
	} {
		if _, err := EncryptMeta([]byte("pw"), nil, testKDF, m); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := EncryptMeta([]byte("pw"), nil, testKDF, Meta{Title: "Café ☕", Description: "a\nb"}); err != nil {
		t.Errorf("unicode and a newline in the description: %v", err)
	}

	// A hostile file claiming enormous labels is rejected without reading them.
	enc, _ := EncryptMeta([]byte("pw"), []byte("x"), testKDF, Meta{Title: "t"})
	bad := bytes.Clone(enc)
	bad[headerLen], bad[headerLen+1] = 0xff, 0xff
	if _, err := ReadMeta(bad); err == nil {
		t.Error("a huge title length was accepted")
	}
	if _, err := Decrypt([]byte("pw"), bad); err == nil {
		t.Error("Decrypt accepted a huge title length")
	}
	for n := 0; n < len(enc); n++ { // truncations never panic
		ReadMeta(enc[:n])
		Decrypt([]byte("pw"), enc[:n])
	}
}

func TestCleanTrimsAndNormalisesLineEnds(t *testing.T) {
	got := Meta{Title: "  hi  ", Description: "\r\n a\r\nb\rc \n"}.Clean()
	if got.Title != "hi" || got.Description != "a\nb\nc" {
		t.Errorf("Clean = %q, %q", got.Title, got.Description)
	}
}

func TestSaveKeepsTheLabelsAndSaveMetaReplacesThem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := SaveMeta(path, []byte("pw"), []byte("one"), nil, Meta{Title: "T", Description: "D"}); err != nil {
		t.Fatal(err)
	}
	// Saving a new version leaves them as they were.
	prev, _ := os.ReadFile(path)
	if err := Save(path, []byte("pw"), []byte("two"), prev); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if m, _ := ReadMeta(data); m != (Meta{Title: "T", Description: "D"}) {
		t.Errorf("labels after Save: %+v", m)
	}
	if bak, _ := os.ReadFile(BackupPath(path)); !bytes.Equal(bak, prev) {
		t.Error("the backup is not the previous version")
	}
	if err := SaveMeta(path, []byte("pw"), []byte("three"), data, Meta{Title: "New"}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if m, _ := ReadMeta(data); m != (Meta{Title: "New"}) {
		t.Errorf("labels after SaveMeta: %+v", m)
	}
}
