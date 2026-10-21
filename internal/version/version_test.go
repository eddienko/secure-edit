package version

import "testing"

func TestString(t *testing.T) {
	if String() == "" {
		t.Fatal("empty version")
	}
	old := Build
	Build = "v9.9.9"
	defer func() { Build = old }()
	if got := String(); got != "v9.9.9" {
		t.Fatalf("got %q", got)
	}
}
