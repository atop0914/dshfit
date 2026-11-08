// Package cli wires the checks to a terminal. Writers are injected so the
// output is testable without capturing a global.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/atop0914/dshfit/internal/compat"
	"github.com/atop0914/dshfit/internal/manifest"
	"github.com/atop0914/dshfit/internal/version"
)

const usage = `dshfit — will these DSH plugins load on that harness version?

usage:
  dshfit check --host <version> [--json] <plugin-dir|package.tgz>...
  dshfit profile --host <version> [--json] <listing.json|->
  dshfit diff --from <version> --to <version> [--json] <listing.json|->
  dshfit version
  dshfit help

check evaluates each plugin against one harness version and exits non-zero when
anything is blocked, so it can gate a CI job or a shell script.

An argument may be an unpacked plugin directory or an npm tarball (.tgz, as
produced by npm pack). The archive is detected by its gzip magic first and by
suffix second, and it answers a question a directory cannot: whether the paths
package.json declares are actually inside the artifact a user downloads.

profile evaluates a whole profile at once and groups the failures by root cause:

  dsh plugin --profile web list --json | dshfit profile --host 0.2.0-rc.2 -

A package with no dsh.bundle.patch is reported as "not a plugin" rather than
blocked: in a profile that is what data and tooling packages look like.

diff answers the third question: you run A, you are considering B, what changes?

  dshfit diff --from 0.2.1-rc.3 --to 0.2.2-rc.1 listing.json

Only packages that differ are printed. A plugin blocked on both hosts is not a
difference; the same plugin blocked for a different reason is — the verdict
looks identical and the fix is the opposite. Both --from and --to are required,
because a default on either side would answer for a version nobody named.

flags for check and profile:
  --host <version>   harness version to test against, e.g. 0.2.0-rc.2 (required)
  --json             emit machine-readable output instead of text

flags for diff:
  --from <version>   harness version the profile runs on today (required)
  --to <version>     harness version being considered (required)
  --json             emit machine-readable output instead of text
`

// warnf writes a diagnostic to stderr and deliberately ignores the write error.
// There is nowhere left to report a broken stderr, and the process exit code
// already carries the outcome; failing to print the reason must not change it.
func warnf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// Run executes one command and returns the process exit code. Stdin is injected
// for the same reason the writers are: `profile -` reads a listing from a pipe,
// and a test that has to swap a process-wide stdin is a test that cannot run in
// parallel with anything else.
func Run(in io.Reader, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		_, _ = io.WriteString(errOut, usage)
		return 2
	}
	switch args[0] {
	case "check":
		return runCheck(args[1:], out, errOut)
	case "profile":
		return runProfile(in, args[1:], out, errOut)
	case "diff":
		return runDiff(in, args[1:], out, errOut)
	case "version", "--version", "-v":
		if _, err := fmt.Fprintln(out, version.String()); err != nil {
			return 2
		}
		return 0
	case "help", "-h", "--help":
		if _, err := io.WriteString(out, usage); err != nil {
			return 2
		}
		return 0
	}
	warnf(errOut, "unknown command %q\n\n%s", args[0], usage)
	return 2
}

func runCheck(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(errOut)
	host := fs.String("host", "", "harness version to test against")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *host == "" {
		warnf(errOut, "check: --host is required\n\n%s", usage)
		return 2
	}
	dirs := fs.Args()
	if len(dirs) == 0 {
		warnf(errOut, "check: give at least one plugin directory or tarball\n")
		return 2
	}

	reports := make([]compat.Report, 0, len(dirs))
	for _, arg := range dirs {
		p, err := manifest.LoadPath(arg)
		if err != nil {
			// An unreadable input is a reportable outcome, not a crash: a
			// profile directory often holds one broken checkout, and the user
			// still needs the verdict for every other plugin in it.
			reports = append(reports, compat.Report{
				Plugin:  arg,
				Dir:     arg,
				Host:    *host,
				Verdict: compat.Blocked,
				Issues: []compat.Issue{{
					Severity: compat.Block,
					Code:     "manifest-unreadable",
					Detail:   err.Error(),
				}},
			})
			continue
		}
		rep, err := compat.Evaluate(p, *host)
		if err != nil {
			warnf(errOut, "check: %v\n", err)
			return 2
		}
		reports = append(reports, rep)
	}

	ok, warn, blocked := compat.Count(reports)

	var werr error
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		werr = enc.Encode(reports)
	} else {
		werr = writeText(out, reports, *host, ok, warn, blocked)
	}
	if werr != nil {
		warnf(errOut, "check: write output: %v\n", werr)
		return 2
	}
	if blocked > 0 {
		return 1
	}
	return 0
}

func writeText(w io.Writer, reports []compat.Report, host string, ok, warn, blocked int) error {
	var b strings.Builder
	for _, r := range reports {
		label := strings.ToUpper(string(r.Verdict))
		origin := r.Dir
		if r.Source == string(manifest.TarballSource) {
			origin += "  (tarball, files[] cross-checked against the archive)"
		}
		fmt.Fprintf(&b, "%-8s %s@%s  %s\n", label, r.Plugin, r.Version, origin)
		for _, is := range r.Issues {
			subject := is.Package
			if subject == "" {
				subject = r.Plugin
			}
			if is.Range != "" {
				subject = fmt.Sprintf("%s %s", subject, is.Range)
			}
			fmt.Fprintf(&b, "  %-5s %-26s %s\n", is.Severity, is.Code, subject)
			fmt.Fprintf(&b, "        %s\n", is.Detail)
		}
	}
	fmt.Fprintf(&b, "\n%d plugin(s) against host %s: %d ok, %d warn, %d blocked\n",
		len(reports), host, ok, warn, blocked)
	if blocked > 0 {
		fmt.Fprintf(&b, "%d plugin(s) will not load. Fix the blocked ones before upgrading.\n", blocked)
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}
	return nil
}
