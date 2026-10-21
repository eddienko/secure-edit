package vault

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// FolderMetaName is the file in a folder that holds its title and description.
// It starts with a dot so that listings hide it, and it moves with the folder.
const FolderMetaName = ".sedit-folder"

// maxFolderMetaFile bounds what is read: far more than any valid title and
// description take, so a hostile file can't make the reader allocate much.
const maxFolderMetaFile = 8 << 10

type folderFile struct {
	Version     int    `json:"version"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// ReadFolderMeta returns the title and description of the folder dir. Unlike a
// file's, they are not encrypted or authenticated: they are plain text next to
// the files, for finding your way around. A missing, oversized, unreadable or
// invalid file, or one that is a symlink, gives none.
func ReadFolderMeta(dir string) Meta {
	path := filepath.Join(dir, FolderMetaName)
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFolderMetaFile {
		return Meta{}
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxFolderMetaFile {
		return Meta{}
	}
	var f folderFile
	if json.Unmarshal(data, &f) != nil || f.Version != 1 {
		return Meta{}
	}
	m := Meta{Title: f.Title, Description: f.Description}
	if m.Validate() != nil {
		return Meta{}
	}
	return m
}

// WriteFolderMeta sets the title and description of the folder dir, replacing
// any. With neither, the file is removed.
func WriteFolderMeta(dir string, m Meta) error {
	m = m.Clean()
	if err := m.Validate(); err != nil {
		return err
	}
	path := filepath.Join(dir, FolderMetaName)
	if m.IsZero() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(folderFile{Version: 1, Title: m.Title, Description: m.Description}, "", "  ")
	if err != nil {
		return err
	}
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		if err := os.Remove(path); err != nil { // a symlink or the like: replace it, never write through it
			return err
		}
	}
	return WriteAtomic(path, append(data, '\n'))
}
