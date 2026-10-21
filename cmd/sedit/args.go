package main

import (
	"fmt"
	"strings"
)

type opts struct {
	mode   string // edit, print, passwd, encrypt, remember, forget, share, import, keygen, write, append, mv, rm, set-default, forget-default
	file   string
	yes    bool
	choice pwChoice

	// --share
	to         []string // --to KEY
	recipFiles []string // -R FILE
	out        string   // -o OUT (--share and --import)
	armor      bool     // -a

	// --import
	identities []string // -i KEY

	// --keygen
	pq bool // --pq

	// --mv, --rm
	dest  string   // --mv OLD NEW: NEW
	files []string // --rm FILE...
}

var modeFlags = map[string]string{
	"-p":               "print",
	"--passwd":         "passwd",
	"--encrypt":        "encrypt",
	"--remember":       "remember",
	"--forget":         "forget",
	"--share":          "share",
	"--import":         "import",
	"--keygen":         "keygen",
	"--mv":             "mv",
	"--rm":             "rm",
	"--write":          "write",
	"--append":         "append",
	"--set-default":    "set-default",
	"--forget-default": "forget-default",
}

// parseArgs turns the command line into opts, or returns the usage text.
func parseArgs(args []string) (opts, error) {
	o := opts{mode: "edit"}
	modeSet, choiceSet := false, false
	var positional []string
	bad := func(msg string) (opts, error) { return opts{}, fmt.Errorf("%s\n\n%s", msg, usage) }

	// value returns the argument after args[i], which belongs to the flag at i.
	value := func(i int) (string, bool) {
		if i+1 >= len(args) {
			return "", false
		}
		return args[i+1], true
	}

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			return opts{mode: "help"}, nil
		case a == "--version":
			return opts{mode: "version"}, nil
		case modeFlags[a] != "":
			if modeSet {
				return bad("only one of -p, --passwd, --encrypt, --remember, --forget, --share, --import, --keygen, --write, --append, --mv, --rm, --set-default, --forget-default may be given")
			}
			o.mode, modeSet = modeFlags[a], true
		case a == "-y":
			o.yes = true
		case a == "-a" || a == "--armor":
			o.armor = true
		case a == "--pq":
			o.pq = true
		case a == "--to" || a == "-R" || a == "-o" || a == "-i":
			v, ok := value(i)
			if !ok {
				return bad(a + " needs a value")
			}
			i++
			switch a {
			case "--to":
				o.to = append(o.to, v)
			case "-R":
				o.recipFiles = append(o.recipFiles, v)
			case "-i":
				o.identities = append(o.identities, v)
			default:
				if o.out != "" {
					return bad("only one -o may be given")
				}
				o.out = v
			}
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
		default:
			positional = append(positional, a)
		}
	}

	switch o.mode {
	case "mv":
		if len(positional) != 2 {
			return bad("--mv needs two arguments: OLD and NEW")
		}
		o.file, o.dest = positional[0], positional[1]
	case "rm":
		if len(positional) == 0 {
			return bad("missing FILE")
		}
		o.files, o.file = positional, positional[0]
	case "set-default", "forget-default", "keygen":
		if len(positional) > 0 {
			return bad(fmt.Sprintf("--%s takes no FILE", o.mode))
		}
	default:
		switch len(positional) {
		case 0:
			return bad("missing FILE")
		case 1:
			o.file = positional[0]
		default:
			return bad("only one FILE may be given")
		}
	}

	switch {
	case o.yes && o.mode != "encrypt" && o.mode != "rm":
		return bad("-y only applies to --encrypt and --rm")
	case o.mode == "share" && len(o.to) == 0 && len(o.recipFiles) == 0:
		return bad("--share needs at least one recipient (--to KEY or -R FILE)")
	case o.mode != "share" && (len(o.to) > 0 || len(o.recipFiles) > 0 || o.armor):
		return bad("--to, -R and -a only apply to --share")
	case o.mode == "keygen" && o.out == "":
		return bad("--keygen needs -o FILE for the private key (-o - prints it to stdout)")
	case o.pq && o.mode != "keygen":
		return bad("--pq only applies to --keygen")
	case o.mode != "share" && o.mode != "import" && o.mode != "keygen" && o.out != "":
		return bad("-o only applies to --share, --import and --keygen")
	case o.mode != "import" && len(o.identities) > 0:
		return bad("-i only applies to --import")
	case choiceSet && o.mode != "edit" && o.mode != "remember" && o.mode != "passwd" && o.mode != "encrypt" && o.mode != "import" && o.mode != "write" && o.mode != "append":
		return bad("--default and --custom only apply when choosing a new password")
	}
	return o, nil
}
