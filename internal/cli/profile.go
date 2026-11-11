package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/atop0914/dshfit/internal/profile"
)

func runProfile(in io.Reader, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("profile", flag.ContinueOnError)
	fs.SetOutput(errOut)
	host := fs.String("host", "", "harness version to test against")
	asJSON := fs.Bool("json", false, "emit JSON (same as --format json)")
	format := fs.String("format", "", "output format: text, markdown or json (default text)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *host == "" {
		warnf(errOut, "profile: --host is required\n\n%s", usage)
		return 2
	}
	// The format is resolved before anything is read, so a typo cannot waste a
	// profile walk and cannot be reported as a listing problem.
	mode, err := profileFormat(*format, *asJSON)
	if err != nil {
		warnf(errOut, "profile: %v\n", err)
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		warnf(errOut, "profile: give exactly one listing (`dsh plugin --profile <name> list --json`), "+
			"or `-` to read it from stdin\n")
		return 2
	}

	projects, base, err := readListing(in, rest[0], "profile")
	if err != nil {
		warnf(errOut, "%v\n", err)
		return 2
	}
	res, err := profile.Assess(projects, *host, base)
	if err != nil {
		warnf(errOut, "profile: %v\n", err)
		return 2
	}

	var werr error
	switch mode {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		werr = enc.Encode(res)
	case "markdown":
		werr = profile.Markdown(out, res)
	default:
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

// profileFormat resolves the two output flags into one mode.
//
// --json predates --format and is kept because every existing script uses it;
// the two are reconciled here rather than in the switch so a contradictory
// pair is a usage error (2) instead of one flag silently winning. Exit 2 is the
// only channel a CI job can tell "the tool is misconfigured" from "the plugins
// are blocked", and that distinction is the whole point of wiring this into CI.
func profileFormat(name string, asJSON bool) (string, error) {
	switch name {
	case "":
		if asJSON {
			return "json", nil
		}
		return "text", nil
	case "text", "markdown", "json":
		if asJSON && name != "json" {
			return "", fmt.Errorf("--json and --format %s ask for different formats; give one", name)
		}
		return name, nil
	default:
		return "", fmt.Errorf("unknown --format %q (want text, markdown or json)", name)
	}
}

// readListing turns the command's single argument into parsed projects plus the
// directory relative package paths resolve against.
//
// It is shared by profile and diff so the two commands cannot disagree about
// what a listing argument means: `-` is stdin, a path is a file whose directory
// becomes the base. The command name is a parameter because it prefixes every
// diagnostic — a message that names the wrong subcommand sends the reader to
// the wrong usage.
func readListing(in io.Reader, arg, cmd string) ([]profile.Project, string, error) {
	if arg == "-" {
		projects, err := profile.Parse(in)
		if err != nil {
			return nil, "", fmt.Errorf("%s: stdin: %w", cmd, err)
		}
		return projects, "", nil
	}
	// Relative package paths in a listing are relative to the listing, and that
	// rule lives in one place so the sidecar cannot answer it differently.
	projects, base, err := profile.LoadListing(arg)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", cmd, err)
	}
	return projects, base, nil
}

func writeProfileText(w io.Writer, res profile.Result) error {
	var b strings.Builder
	c := res.Counts
	fmt.Fprintf(&b, "%s against host %s — %d package(s)\n",
		strings.Join(res.Projects, ", "), res.Host, c.Packages)

	blocked, warn := profile.SplitBySeverity(res.Groups)
	if len(blocked) > 0 {
		fmt.Fprintf(&b, "\nblocked by root cause — %d cause(s), %d plugin(s)\n",
			len(blocked), profile.DistinctPluginCount(blocked))
		writeGroups(&b, blocked)
	}
	if len(warn) > 0 {
		fmt.Fprintf(&b, "\nwarnings by root cause — %d cause(s), %d plugin(s)\n",
			len(warn), profile.DistinctPluginCount(warn))
		writeGroups(&b, warn)
	}

	if unknown := res.EntriesWithStatus(profile.StatusUnknown); len(unknown) > 0 {
		fmt.Fprintf(&b, "\ncould not be judged — %d package(s)\n", len(unknown))
		for _, e := range unknown {
			fmt.Fprintf(&b, "  %s%s  %s\n", e.Name, versionSuffix(e), e.Dir)
			fmt.Fprintf(&b, "    %s\n", e.Reason)
		}
	}
	// Plugins with no findings are listed too. A report that only prints
	// problems leaves the reader unable to tell "checked and fine" from "never
	// looked at", which is the difference the whole tool is about.
	if clean := res.CleanPlugins(); len(clean) > 0 {
		fmt.Fprintf(&b, "\nno findings — %d plugin(s)\n", len(clean))
		for _, e := range clean {
			fmt.Fprintf(&b, "  %s%s\n", e.Name, versionSuffix(e))
		}
	}
	if notPlugins := res.EntriesWithStatus(profile.StatusNotAPlugin); len(notPlugins) > 0 {
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
		writeFix(b, g)
	}
}

// writeFix prints the solver's answer under a group's prose. The two parts have
// different jobs: Fix says which direction to move and why, these say exactly
// what to write and what the written range promises about versions that do not
// exist yet. Both are printed because a reader who only sees the range will
// assume it solves more than it does.
func writeFix(b *strings.Builder, g profile.Group) {
	if len(g.FixCandidates) > 0 {
		best := g.FixCandidates[0]
		fmt.Fprintf(b, "    smallest fix: %s\n", best.Range)
		if best.Note != "" {
			fmt.Fprintf(b, "      %s\n", best.Note)
		}
		if rest := g.FixCandidates[1:]; len(rest) > 0 {
			fmt.Fprintf(b, "    other verified fixes:\n")
			for _, c := range rest {
				fmt.Fprintf(b, "      %s\n", c.Range)
				if c.Note != "" {
					fmt.Fprintf(b, "        %s\n", c.Note)
				}
			}
		}
	} else if g.FixRefusal != "" {
		// No candidate is a real outcome: the range does not parse, or it already
		// admits the host. Printing a guess here would be worse than printing
		// nothing, so the refusal is printed.
		fmt.Fprintf(b, "    smallest fix: none — %s\n", g.FixRefusal)
	}
	for _, a := range g.FixTried {
		fmt.Fprintf(b, "    not a fix: %s\n", a.Range)
		fmt.Fprintf(b, "      %s\n", a.Why)
	}
}

func versionSuffix(e profile.Entry) string {
	if e.Version == "" {
		return ""
	}
	return "@" + e.Version
}
