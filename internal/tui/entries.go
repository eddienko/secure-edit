// Package tui is a full-screen terminal interface for sedit files: a file list
// on the left and, on the right, a preview that stays locked until you ask for
// it. All the real work is done by the same packages the command line uses.
package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eddienko/secure-edit/internal/vault"
)

// Kind says what a directory entry is.
type Kind int

const (
	KindDir   Kind = iota // a directory
	KindSedit             // a sedit file
	KindAge               // an age-encrypted file (see sedit --import)
	KindOther             // anything else
)

// Entry is one visible item in a directory listing.
type Entry struct {
	Name string
	Path string
	Kind Kind
	Size int64
}

// KindOf looks at the first bytes of the file at path.
func KindOf(path string) Kind {
	f, err := os.Open(path)
	if err != nil {
		return KindOther
	}
	defer f.Close()
	head := make([]byte, 64)
	n, _ := f.Read(head)
	head = head[:n]
	switch {
	case vault.IsSedit(head):
		return KindSedit
	case bytes.HasPrefix(head, []byte("age-encryption.org/")),
		bytes.HasPrefix(bytes.TrimLeft(head, " \t\r\n"), []byte("-----BEGIN AGE ENCRYPTED FILE-----")):
		return KindAge
	}
	return KindOther
}

// hidden reports whether a name is bookkeeping rather than something to show:
// dot files (which covers the lock files and temp files sedit leaves next to
// yours) and FILE.bak backups.
func hidden(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".bak")
}

// List returns the visible entries of dir: directories first, then files, each
// group in case-insensitive name order. Symlinks are followed for the purpose of
// telling what they point to.
func List(dir string) ([]Entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, de := range des {
		if hidden(de.Name()) {
			continue
		}
		path := filepath.Join(dir, de.Name())
		fi, err := os.Stat(path) // follows symlinks
		e := Entry{Name: de.Name(), Path: path, Kind: KindOther}
		switch {
		case err != nil: // a dangling symlink, say: show it, but as "other"
		case fi.IsDir():
			e.Kind = KindDir
		case fi.Mode().IsRegular():
			e.Kind, e.Size = KindOf(path), fi.Size()
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := out[i].Kind == KindDir, out[j].Kind == KindDir
		if di != dj {
			return di
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}
