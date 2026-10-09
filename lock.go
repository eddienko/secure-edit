package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ErrLocked is returned when another sedit process holds the lock.
type ErrLocked struct {
	path string
	pid  string // may be empty if unknown
}

func (e *ErrLocked) Error() string {
	if e.pid != "" {
		return fmt.Sprintf("%s is being edited by another sedit (pid %s)", e.path, e.pid)
	}
	return fmt.Sprintf("%s is being edited by another sedit", e.path)
}

// lockPath returns the sidecar lock file for path: dir/.name.lock
func lockPath(path string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".lock")
}

// Lock takes an exclusive, non-blocking flock on a sidecar file. The kernel
// drops the lock when the process dies, so a crash never leaves a stale lock.
// The lock file itself is left in place on purpose: unlinking a flocked file
// would let two processes lock different inodes under the same name.
func Lock(path string) (unlock func(), err error) {
	lp := lockPath(path)
	f, err := os.OpenFile(lp, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		defer f.Close()
		if err == syscall.EWOULDBLOCK {
			b, _ := os.ReadFile(lp)
			return nil, &ErrLocked{path: path, pid: strings.TrimSpace(string(b))}
		}
		return nil, err
	}
	// Informational only; the flock is the source of truth.
	f.Truncate(0)
	f.WriteString(strconv.Itoa(os.Getpid()) + "\n")
	return func() {
		f.Truncate(0)
		f.Close() // releases the flock
	}, nil
}
