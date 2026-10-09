package main

import (
	"bytes"
	"errors"
	"testing"
)

// Cheap parameters so tests run fast.
var testKDF = kdfParams{1, 64, 1}

func TestRoundTrip(t *testing.T) {
	pt := []byte("hunter2\nline two\n")
	enc, err := Encrypt([]byte("pw"), pt, testKDF)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decrypt([]byte("pw"), enc)
	if err != nil || !bytes.Equal(got, pt) {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestEmptyPlaintext(t *testing.T) {
	enc, _ := Encrypt([]byte("pw"), nil, testKDF)
	got, err := Decrypt([]byte("pw"), enc)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestWrongPassword(t *testing.T) {
	enc, _ := Encrypt([]byte("pw"), []byte("secret"), testKDF)
	if _, err := Decrypt([]byte("nope"), enc); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("want ErrBadPassword, got %v", err)
	}
}

func TestTamperDetected(t *testing.T) {
	enc, _ := Encrypt([]byte("pw"), []byte("secret"), testKDF)
	for _, i := range []int{7, 12, 16, headerLen, len(enc) - 1} { // params, salt, body, tag
		bad := bytes.Clone(enc)
		bad[i] ^= 1
		if _, err := Decrypt([]byte("pw"), bad); err == nil {
			t.Errorf("tampering at byte %d not detected", i)
		}
	}
}

func TestNotASeditFile(t *testing.T) {
	if _, err := Decrypt([]byte("pw"), bytes.Repeat([]byte("x"), 200)); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Decrypt([]byte("pw"), []byte("short")); err == nil {
		t.Fatal("expected error")
	}
}

func TestFreshSaltAndNonce(t *testing.T) {
	a, _ := Encrypt([]byte("pw"), []byte("x"), testKDF)
	b, _ := Encrypt([]byte("pw"), []byte("x"), testKDF)
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions identical")
	}
}
