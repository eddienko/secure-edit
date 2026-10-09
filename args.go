package main

import (
	"fmt"
	"strings"
)

type opts struct {
	mode   string // edit, print, passwd, encrypt, remember, forget, set-default, forget-default
	file   string
	yes    bool
	choice pwChoice
}

var modeFlags = map[string]string{
	"-p":               "print",
	"--passwd":         "passwd",
	"--encrypt":        "encrypt",
	"--remember":       "remember",
	"--forget":         "forget",
	"--set-default":    "set-default",
	"--forget-default": "forget-default",
}

// parseArgs turns the command line into opts, or returns the usage text.
func parseArgs(args []string) (opts, error) {
	o := opts{mode: "edit"}
	modeSet, choiceSet := false, false
	bad := func(msg string) (opts, error) { return opts{}, fmt.Errorf("%s\n\n%s", msg, usage) }

	for _, a := range args {
		switch {
		case modeFlags[a] != "":
			if modeSet {
				return bad("only one of -p, --passwd, --encrypt, --remember, --forget, --set-default, --forget-default may be given")
			}
			o.mode, modeSet = modeFlags[a], true
		case a == "-y":
			o.yes = true
		case a == "--default" || a == "--custom":
			c := choiceDefault
			if a == "--custom" {
				c = choiceCustom
			}
			if choiceSet && o.choice != c {
				return bad("--default and --custom can't be combined")
			}
			o.choice, choiceSet = c, true
		case strings.HasPrefix(a, "-"):
			return bad("unknown option " + a)
		case o.file != "":
			return bad("only one FILE may be given")
		default:
			o.file = a
		}
	}

	needsFile := o.mode != "set-default" && o.mode != "forget-default"
	switch {
	case needsFile && o.file == "":
		return bad("missing FILE")
	case !needsFile && o.file != "":
		return bad(fmt.Sprintf("--%s takes no FILE", o.mode))
	case o.yes && o.mode != "encrypt":
		return bad("-y only applies to --encrypt")
	case choiceSet && o.mode != "edit" && o.mode != "remember" && o.mode != "passwd" && o.mode != "encrypt":
		return bad("--default and --custom only apply when choosing a new password")
	}
	return o, nil
}
