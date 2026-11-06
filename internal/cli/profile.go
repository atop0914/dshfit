package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/atop0914/dshfit/internal/compat"
	"github.com/atop0914/dshfit/internal/profile"
)

func runProfile(in io.Reader, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("profile", flag.ContinueOnError)
	fs.SetOutput(errOut)
	host := fs.String("host", "", "harness version to test against")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *host == "" {
		warnf(errOut, "profile: --host is required\n\n%s", usage)
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		warnf(errOut, "profile: give exactly one listing (`dsh plugin --profile <name> list --json`), "+
			"or `-` to read it from stdin\n")
		return 2
	}

	src := in
	from := "stdin"
	base := ""
	if rest[0] != "-" {
		f, err := os.Open(rest[0])
		if err != nil {
			warnf(errOut, "profile: read listing: %v\n", err)
			return 2
		}
		defer func() { _ = f.Close() }()
		src = f
		from = rest[0]
		// Relative package paths in a listing are relative to the listing.
		if abs, err := filepath.Abs(rest[0]); err == nil {
			base = filepath.Dir(abs)
		}
	}

	projects, err := profile.Parse(src)
	if err != nil {
		warnf(errOut, "profile: %s: %v\n", from, err)
		return 2
	}
	res, err := profile.Assess(projects, *host, base)
	if err != nil {
		warnf(errOut, "profile: %v\n", err)
		return 2
	}

	var werr error
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		werr = enc.Encode(res)
	} else {
		werr = writeProfileText(out, res)
	}
	if werr != nil {
		warnf(errOut, "profile: write output: %v\n", werr)
		return 2
	}
	// Unjudged packages fail the run too. An exit code that says "fine" while a
	// package could not be read is the one outcome the tool must never produce:
	// the caller cannot distinguish it from a clean bill.
	if res.Counts.Blocked > 0 || res.Counts.Unknown > 0 {
		return 1
	}
	return 0
}

func writeProfileText(w io.Writer, res profile.Result) error {
	var b strings.Builder
	c := res.Counts
	fmt.Fprintf(&b, "%s against host %s — %d package(s)\n",
		strings.Join(res.Projects, ", "), res.Host, c.Packages)

	blocked, warn := splitGroups(res.Groups)
	if len(blocked) > 0 {
		fmt.Fprintf(&b, "\nblocked by root cause — %d cause(s), %d plugin(s)\n",
			len(blocked), distinctPlugins(blocked))
		writeGroups(&b, blocked)
	}
	if len(warn) > 0 {
		fmt.Fprintf(&b, "\nwarnings by root cause — %d cause(s), %d plugin(s)\n",
			len(warn), distinctPlugins(warn))
		writeGroups(&b, warn)
	}

	if unknown := entriesWithStatus(res.Entries, profile.StatusUnknown); len(unknown) > 0 {
		fmt.Fprintf(&b, "\ncould not be judged — %d package(s)\n", len(unknown))
		for _, e := range unknown {
			fmt.Fprintf(&b, "  %s%s  %s\n", e.Name, versionSuffix(e), e.Dir)
			fmt.Fprintf(&b, "    %s\n", e.Reason)
		}
	}
	// Plugins with no findings are listed too. A report that only prints
	// problems leaves the reader unable to tell "checked and fine" from "never
	// looked at", which is the difference the whole tool is about.
	if clean := cleanPlugins(res.Entries); len(clean) > 0 {
		fmt.Fprintf(&b, "\nno findings — %d plugin(s)\n", len(clean))
		for _, e := range clean {
			fmt.Fprintf(&b, "  %s%s\n", e.Name, versionSuffix(e))
		}
	}
	if notPlugins := entriesWithStatus(res.Entries, profile.StatusNotAPlugin); len(notPlugins) > 0 {
		fmt.Fprintf(&b, "\nnot a plugin (no dsh.bundle.patch, nothing to mount) — %d package(s)\n",
			len(notPlugins))
		for _, e := range notPlugins {
			fmt.Fprintf(&b, "  %s%s\n", e.Name, versionSuffix(e))
		}
	}

	fmt.Fprintf(&b, "\n%d package(s): %d ok, %d warn, %d blocked, %d unknown, %d not a plugin\n",
		c.Packages, c.OK, c.Warn, c.Blocked, c.Unknown, c.NotAPlugin)
	if c.Blocked > 0 {
		fmt.Fprintf(&b, "%d plugin(s) will not load on %s. Fix the blocked causes before upgrading.\n",
			c.Blocked, res.Host)
	}
	if c.Unknown > 0 {
		fmt.Fprintf(&b, "%d package(s) could not be judged; that is not the same as compatible.\n", c.Unknown)
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}
	return nil
}

func writeGroups(b *strings.Builder, groups []profile.Group) {
	for _, g := range groups {
		fmt.Fprintf(b, "  [%s] %s", g.Severity, g.Code)
		if g.Range != "" {
			fmt.Fprintf(b, "  range=%s", g.Range)
		}
		if g.Reason != "" {
			fmt.Fprintf(b, "  reason=%s", g.Reason)
		}
		fmt.Fprintf(b, "\n    plugins (%d): %s\n", len(g.Plugins), strings.Join(g.Plugins, ", "))
		if len(g.Targets) > 0 {
			fmt.Fprintf(b, "    rejects (%d):\n", len(g.Targets))
			for _, t := range g.Targets {
				fmt.Fprintf(b, "      %s\n", t)
			}
		}
		fmt.Fprintf(b, "    fix: %s\n", g.Fix)
	}
}

func splitGroups(groups []profile.Group) (blocked, warn []profile.Group) {
	for _, g := range groups {
		if g.Severity == compat.Block {
			blocked = append(blocked, g)
			continue
		}
		warn = append(warn, g)
	}
	return blocked, warn
}

func distinctPlugins(groups []profile.Group) int {
	seen := map[string]bool{}
	for _, g := range groups {
		for _, p := range g.Plugins {
			seen[p] = true
		}
	}
	return len(seen)
}

func entriesWithStatus(entries []profile.Entry, status profile.Status) []profile.Entry {
	var out []profile.Entry
	for _, e := range entries {
		if e.Status == status {
			out = append(out, e)
		}
	}
	return out
}

// cleanPlugins returns judged plugins with the ok verdict, in listing order.
func cleanPlugins(entries []profile.Entry) []profile.Entry {
	var out []profile.Entry
	for _, e := range entries {
		if e.Status == profile.StatusPlugin && e.Report != nil && e.Report.Verdict == compat.OK {
			out = append(out, e)
		}
	}
	return out
}

func versionSuffix(e profile.Entry) string {
	if e.Version == "" {
		return ""
	}
	return "@" + e.Version
}
