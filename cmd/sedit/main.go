// Command sedit edits passphrase-encrypted text files. Run bare, or on a folder,
// it opens a full-screen interface for browsing and managing them; given a file
// or an option it is a command-line tool.
package main

import (
	"fmt"
	"os"

	"github.com/eddienko/secure-edit/internal/cli"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sedit:", err)
		os.Exit(1)
	}
}

// run opens the interface if the arguments ask for it, and otherwise hands them
// to the command line.
func run(args []string) error {
	ui, err := parseUI(args)
	if err != nil {
		return err
	}
	if ui == nil {
		return cli.Run(args)
	}
	return runUI(ui)
}
