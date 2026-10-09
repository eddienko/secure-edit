package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
)

// buildVersion can be set at build time:
//
//	go build -ldflags "-X main.buildVersion=v1.2.3"
var buildVersion = ""

func versionString() string {
	if buildVersion != "" {
		return buildVersion
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v // installed with "go install ...@version"
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	return "dev-" + rev + dirty
}

// resolvePath follows a symlink to the real file, so locking, backups and the
// atomic rename all act on the target instead of replacing the link. Paths
// that don't exist yet are returned unchanged.
func resolvePath(path string) (string, error) {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("%s is a symlink whose target can't be resolved: %w", path, err)
	}
	return real, nil
}
