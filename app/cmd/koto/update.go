package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime"
	"time"

	"koto/internal/update"
)

const updateHelp = `Usage: koto update [--check] [--channel stable|beta] [--json]

Check published releases and update this CLI executable. The desktop app is separate.
  --check    Check only; do not download or replace anything.
  --channel  Stable or beta; includes stable and beta releases by default.
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
	fail := func(err error, stage string) int {
		if *asJSON {
			failure := map[string]any{"ok": false, "error": err.Error(), "stage": stage, "url": update.ReleasesPage}
			var unpublished *update.NoPublishedReleaseError
			if errors.As(err, &unpublished) {
				failure["code"], failure["channel"] = "NO_PUBLISHED_RELEASE", unpublished.Channel
			}
			if errors.Is(err, update.ErrStaleFeed) {
				failure["code"] = "STALE_FEED"
			}
			var request *update.RequestError
			if errors.As(err, &request) {
				failure["code"] = request.Code()
				if !request.RetryAt.IsZero() {
					failure["retryAt"] = request.RetryAt.UnixMilli()
				}
			}
			_ = json.NewEncoder(out).Encode(failure)
		} else {
			label := "Update failed:"
			if stage == "check" {
				label = "Unable to check for updates:"
			}
			var unpublished *update.NoPublishedReleaseError
			if errors.As(err, &unpublished) {
				fmt.Fprintf(errOut, "No published %s release is available yet. See %s.\n", unpublished.Channel, update.ReleasesPage)
			} else {
				fmt.Fprintln(errOut, label, err)
			}
		}
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	client := update.NewClient()
	r, err := client.Check(ctx, version, update.CLI, runtime.GOOS, runtime.GOARCH, *channel)
	if err != nil {
		return fail(err, "check")
	}
	installed := false
	if r.Available && !*check {
		location, err := update.Locate(update.CLI)
		if err != nil {
			return fail(err, "install")
		}
		prepared, err := client.Prepare(ctx, r, location, nil)
		if err != nil {
			return fail(err, "install")
		}
		defer prepared.Close()
		if err := prepared.InstallContext(ctx); err != nil {
			return fail(err, "install")
		}
		if err := prepared.Commit(); err != nil {
			return fail(err, "install")
		}
		installed = true
	}
	label := r.Latest
	if r.Prerelease {
		label += " (beta)"
	}
	if *asJSON {
		_ = json.NewEncoder(out).Encode(map[string]any{"ok": true, "current": r.Current, "latest": r.Latest, "available": r.Available, "updated": installed, "url": r.URL, "prerelease": r.Prerelease, "ahead": update.Newer(r.Current, r.Latest)})
	} else if installed {
		fmt.Fprintf(out, "Updated koto CLI to %s.\n", label)
	} else if r.Available {
		fmt.Fprintf(out, "koto %s is available (installed: %s). Run koto update to install it.\n", label, r.Current)
	} else {
		fmt.Fprintf(out, "Latest published release: %s.\nNo release is newer than installed koto %s.\n", label, r.Current)
	}
	return 0
}
