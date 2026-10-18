package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"runtime"
	"time"

	"koto/internal/update"
)

const updateHelp = `Usage: koto update [--check] [--channel stable|beta] [--json]

Check GitHub Releases and update this CLI executable. The desktop app is separate.
  --check    Check only; do not download or replace anything.
  --channel  Stable or beta; defaults to the installed version's channel.
  --json     Print the result as JSON.
`

func runUpdate(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(errOut)
	check := fs.Bool("check", false, "")
	channel := fs.String("channel", "auto", "")
	asJSON := fs.Bool("json", false, "")
	help := fs.Bool("help", false, "")
	fs.Usage = func() { fmt.Fprint(out, updateHelp) }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *help {
		fmt.Fprint(out, updateHelp)
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprint(errOut, updateHelp)
		return 2
	}
	fail := func(err error) int {
		if *asJSON {
			_ = json.NewEncoder(out).Encode(map[string]any{"ok": false, "error": err.Error()})
		} else {
			fmt.Fprintln(errOut, "Update failed:", err)
		}
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	client := update.NewClient()
	r, err := client.Check(ctx, version, update.CLI, runtime.GOOS, runtime.GOARCH, *channel)
	if err != nil {
		return fail(err)
	}
	installed := false
	if r.Available && !*check {
		location, err := update.Locate(update.CLI)
		if err != nil {
			return fail(err)
		}
		prepared, err := client.Prepare(ctx, r, location, nil)
		if err != nil {
			return fail(err)
		}
		defer prepared.Close()
		if err := prepared.InstallContext(ctx); err != nil {
			return fail(err)
		}
		if err := prepared.Commit(); err != nil {
			return fail(err)
		}
		installed = true
	}
	if *asJSON {
		_ = json.NewEncoder(out).Encode(map[string]any{"ok": true, "current": r.Current, "latest": r.Latest, "available": r.Available, "updated": installed, "url": r.URL})
	} else if installed {
		fmt.Fprintf(out, "Updated koto CLI to %s.\n", r.Latest)
	} else if r.Available {
		fmt.Fprintf(out, "koto %s is available (installed: %s). Run koto update to install it.\n", r.Latest, r.Current)
	} else {
		fmt.Fprintf(out, "koto %s is up to date.\n", r.Current)
	}
	return 0
}
