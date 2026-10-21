// Package version says which version of sedit this is.
package version

import (
	"runtime/debug"
)

// Build can be set at build time:
//
//	go build -ldflags "-X github.com/eddienko/secure-edit/internal/version.Build=v1.2.3"
var Build = ""

// String is the version: the one set at build time, else the module version when
// installed with "go install", else the commit the binary was built from.
func String() string {
	if Build != "" {
		return Build
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
