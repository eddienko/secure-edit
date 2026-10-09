//go:build darwin

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"testing"
)

// Talks to the real login keychain using a throwaway account that is removed
// afterwards.
func TestKeychainStoreRoundTrip(t *testing.T) {
	s := newStore()
	acct := fmt.Sprintf(`/tmp/sedit test "quoted" \ path %d`, os.Getpid())
	t.Cleanup(func() { s.Delete(acct) })

	if _, err := s.Get(acct); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	pw := []byte("pa ss\"w\\ord\nünï\x00")
	if err := s.Set(acct, pw); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(acct)
	if err != nil || !bytes.Equal(got, pw) {
		t.Fatalf("got %q, %v", got, err)
	}
	if err := s.Set(acct, []byte("second")); err != nil { // -U updates
		t.Fatal(err)
	}
	if got, _ := s.Get(acct); string(got) != "second" {
		t.Fatalf("update failed: %q", got)
	}
	if err := s.Delete(acct); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(acct); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
