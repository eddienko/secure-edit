package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/eddienko/secure-edit/internal/fileops"
)

// also describes what was moved or removed along with the file itself.
func also(res fileops.Result) string {
	var parts []string
	if res.Backup {
		parts = append(parts, "its backup")
	}
	if res.Entry {
		parts = append(parts, "its stored password")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (and " + strings.Join(parts, " and ") + ")"
}

func warnAll(res fileops.Result) {
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, "sedit: warning:", w)
	}
}

// mv renames a sedit file, taking its backup and stored password along.
func mv(oldPath, newPath string) error {
	dest, res, err := fileops.Move(oldPath, newPath)
	warnAll(res)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "sedit: moved %s to %s%s\n", oldPath, dest, also(res))
	return nil
}

// rm deletes sedit files with their backups and stored passwords. Every file is
// checked first, so one bad name deletes nothing.
func rm(files []string, yes bool) error {
	var problems []string
	for _, f := range files {
		if err := fileops.Check(f); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("nothing was deleted:\n  %s", strings.Join(problems, "\n  "))
	}

	if !yes {
		what := files[0]
		if len(files) > 1 {
			what = fmt.Sprintf("%d files (%s)", len(files), strings.Join(files, ", "))
		}
		msg := fmt.Sprintf("Delete %s, with its backup and stored password? This can't be undone, and it is not a secure erase: "+
			"the encrypted data may remain in backups and on disk. [y/N] ", what)
		if err := confirm(msg); err != nil {
			return err
		}
	}

	failed := 0
	for _, f := range files {
		res, err := fileops.Remove(f)
		warnAll(res)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sedit: %s: %v\n", f, err)
			failed++
			continue
		}
		fmt.Fprintf(os.Stderr, "sedit: removed %s%s\n", f, also(res))
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d files could not be removed", failed, len(files))
	}
	return nil
}
