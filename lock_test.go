package main

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestLockExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets")
	unlock, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Lock(path)
	var locked *ErrLocked
	if !errors.As(err, &locked) {
		t.Fatalf("want ErrLocked, got %v", err)
	}
	unlock()
	unlock2, err := Lock(path)
	if err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}
	unlock2()
}

func TestLocksIndependentPerFile(t *testing.T) {
	dir := t.TempDir()
	u1, err := Lock(filepath.Join(dir, "a"))
	if err != nil {
		t.Fatal(err)
	}
	defer u1()
	u2, err := Lock(filepath.Join(dir, "b"))
	if err != nil {
		t.Fatal(err)
	}
	u2()
}
