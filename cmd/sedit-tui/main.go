// Command sedit-tui is a full-screen terminal interface for sedit files.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"github.com/eddienko/secure-edit/internal/tui"
)

// buildVersion can be set at build time:
//
//	go build -ldflags "-X main.buildVersion=v1.2.3"
var buildVersion = ""

func version() string {
	if buildVersion != "" {
		return buildVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	fs := flag.NewFlagSet("sedit-tui", flag.ContinueOnError)
	idle := fs.Duration("idle", tui.DefaultIdle, "lock an unlocked file after this long without a key press")
	clipTTL := fs.Duration("clipboard-timeout", tui.DefaultClipboardTTL, "clear text copied with y from the clipboard after this long")
	showVersion := fs.Bool("version", false, "show the version")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `usage: sedit-tui [--idle DURATION] [--clipboard-timeout DURATION] [DIR]

A terminal interface for sedit files: a list of files on the left and, on the
right, a preview that stays locked until you press Enter. DIR is the folder to
show (default: the current one). Press ? for the keys.`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if *showVersion {
		fmt.Println("sedit-tui", version())
		return
	}
	if fs.NArg() > 1 {
		fs.Usage()
		os.Exit(2)
	}
	dir := "."
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}
	if *idle < time.Second {
		fmt.Fprintln(os.Stderr, "sedit-tui: --idle must be at least 1s")
		os.Exit(2)
	}
	if *clipTTL < time.Second {
		fmt.Fprintln(os.Stderr, "sedit-tui: --clipboard-timeout must be at least 1s")
		os.Exit(2)
	}
	app, err := tui.New(dir, tui.Options{Idle: *idle, ClipboardTTL: *clipTTL})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sedit-tui:", err)
		os.Exit(1)
	}
	if err := app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "sedit-tui:", err)
		os.Exit(1)
	}
}
