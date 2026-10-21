// Package tui is a full-screen terminal interface for sedit files: a file list
// on the left and, on the right, a preview that stays locked until you ask for
// it. All the real work is done by the same packages the command line uses.
package tui

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	// ModTime is when the file last changed.
	ModTime time.Time
	Meta    vault.Meta // the title and description: a sedit file's, from its header, or a folder's, from its label file
}

// sortKey is what the list is ordered by: the title if the file has one, since
// that is what is shown, and the name otherwise.
func (e Entry) sortKey() string {
	if t := strings.TrimSpace(e.Meta.Title); t != "" {
		return strings.ToLower(t)
	}
	return strings.ToLower(e.Name)
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

// maxHeader is how much of a file is read to get at its title and description:
// more than any header can be.
const maxHeader = 2400

// metaOf reads the title and description from the header of the sedit file at
// path. They are not authenticated until the file is opened, so they are only
// ever used as labels. A file without them, or one that can't be read, gives
// none.
func metaOf(path string) vault.Meta {
	f, err := os.Open(path)
	if err != nil {
		return vault.Meta{}
	}
	defer f.Close()
	head := make([]byte, maxHeader)
	n, _ := io.ReadFull(f, head)
	m, _ := vault.ReadMeta(head[:n])
	return m
}

// hidden reports whether a name is bookkeeping rather than something to show:
// dot files (which covers the lock files and temp files sedit leaves next to
// yours) and FILE.bak backups.
func hidden(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".bak")
}

// List returns the visible entries of dir: directories first, then files, each
// group in case-insensitive order of what is shown (a file's title, or else its
// name). Symlinks are followed for the purpose of
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
			e.Meta = vault.ReadFolderMeta(path)
		case fi.Mode().IsRegular():
			e.Kind, e.Size, e.ModTime = KindOf(path), fi.Size(), fi.ModTime()
			if e.Kind == KindSedit {
				e.Meta = metaOf(path)
			}
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := out[i].Kind == KindDir, out[j].Kind == KindDir
		if di != dj {
			return di
		}
		if ki, kj := out[i].sortKey(), out[j].sortKey(); ki != kj {
			return ki < kj
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}
