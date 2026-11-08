package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/atop0914/dshfit/internal/profile"
)

func runDiff(in io.Reader, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(errOut)
	from := fs.String("from", "", "harness version the profile runs on today")
	to := fs.String("to", "", "harness version being considered")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// Neither flag has a default, and that is not laziness: this command hands
	// out upgrade advice, so guessing either side would answer a question about
	// a version the user never named.
	if *from == "" || *to == "" {
		warnf(errOut, "diff: --from and --to are both required\n\n%s", usage)
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		warnf(errOut, "diff: give exactly one listing (`dsh plugin --profile <name> list --json`), "+
			"or `-` to read it from stdin\n")
		return 2
	}

	projects, base, err := readListing(in, rest[0], "diff")
	if err != nil {
		warnf(errOut, "%v\n", err)
		return 2
	}
	// Two assessments, one listing. The diff never judges a range itself: two
	// judges in one tool drift apart, and the first symptom would be a diff
	// that contradicts a plain run.
	before, err := profile.Assess(projects, *from, base)
	if err != nil {
		warnf(errOut, "diff: %v\n", err)
		return 2
	}
	after, err := profile.Assess(projects, *to, base)
	if err != nil {
		warnf(errOut, "diff: %v\n", err)
		return 2
	}
	d := profile.Compare(before, after)

	var werr error
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		werr = enc.Encode(d)
	} else {
		werr = writeDiffText(out, d)
	}
	if werr != nil {
		warnf(errOut, "diff: write output: %v\n", werr)
		return 2
	}
	// Exit codes answer "did the upgrade change anything that can break me",
	// not "is the target host healthy":
	//   nothing differs        -> 0, even if packages are blocked on both hosts
	//                             (the upgrade does not make it worse, and the
	//                             footer points at `profile` for the rest)
	//   something differs and the target host has blocked/unjudged -> 1
	//   something differs but the target host is clean -> 0
	// Packages that are only in the source listing never fail the run: they are
	// gone after the upgrade, so they cannot block it.
	if len(d.Changes) == 0 {
		return 0
	}
	if d.ToCounts.Blocked > 0 || d.ToCounts.Unknown > 0 {
		return 1
	}
	return 0
}

// writeDiffText prints the three questions in the order a reader asks them:
// what breaks, what heals, what changed quietly.
func writeDiffText(w io.Writer, d profile.Diff) error {
	var b strings.Builder
	label := strings.Join(d.Projects, ", ")
	if label == "" {
		label = "(unnamed project)"
	}
	fmt.Fprintf(&b, "%s · host %s → %s — %d package(s), %d differ\n",
		label, d.From, d.To, d.ToCounts.Packages, d.Counts.Total())

	if len(d.Changes) == 0 {
		// One line, not an empty table: an empty table is indistinguishable from
		// a report that failed to render, and this is a positive finding.
		b.WriteString("\nno differences\n")
		var caveats []string
		if d.ToCounts.Blocked > 0 {
			caveats = append(caveats, fmt.Sprintf("%d block(s)", d.ToCounts.Blocked))
		}
		if d.ToCounts.Unknown > 0 {
			caveats = append(caveats, fmt.Sprintf("%d unjudged package(s)", d.ToCounts.Unknown))
		}
		if len(caveats) > 0 {
			fmt.Fprintf(&b, "note: %s on %s before and after — this says the upgrade changes nothing, "+
				"not that the profile is healthy. Run `dshfit profile --host %s` for that answer.\n",
				strings.Join(caveats, " and "), d.To, d.To)
		}
		_, err := io.WriteString(w, b.String())
		return err
	}

	sections := []struct {
		kind  profile.ChangeKind
		title string
	}{
		{profile.KindWorse, "will stop loading"},
		{profile.KindBetter, "improves"},
		{profile.KindCause, "root cause changed"},
		{profile.KindAdded, "only in the target listing"},
		{profile.KindRemoved, "only in the source listing"},
	}
	for _, sec := range sections {
		changes := changesOfKind(d.Changes, sec.kind)
		if len(changes) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s — %d\n", sec.title, len(changes))
		for _, ch := range changes {
			writeChange(&b, ch)
		}
	}

	c := d.ToCounts
	fmt.Fprintf(&b, "\n%d package(s) on %s: %d ok, %d warn, %d blocked, %d unknown, %d not a plugin\n",
		c.Packages, d.To, c.OK, c.Warn, c.Blocked, c.Unknown, c.NotAPlugin)
	if c.Blocked > 0 {
		fmt.Fprintf(&b, "`dshfit profile --host %s` prints the cause and the smallest fix for each block.\n", d.To)
	}
	if c.Unknown > 0 {
		fmt.Fprintf(&b, "%d package(s) could not be judged on %s; that is not the same as compatible.\n",
			c.Unknown, d.To)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeChange(b *strings.Builder, ch profile.Change) {
	ver := ""
	if ch.To.Version != "" {
		ver = "@" + ch.To.Version
	} else if ch.From.Version != "" {
		ver = "@" + ch.From.Version
	}
	fmt.Fprintf(b, "  %s%s  %s → %s\n", ch.Name, ver, sideLabel(ch.From), sideLabel(ch.To))

	switch ch.Kind {
	case profile.KindCause:
		// Both sides are printed: the point of this kind is that the verdict
		// stayed the same while the reason moved, and the two reasons call for
		// opposite edits.
		fmt.Fprintf(b, "    before: %s\n", explain(ch.From))
		fmt.Fprintf(b, "    after:  %s\n", explain(ch.To))
	case profile.KindWorse, profile.KindAdded:
		fmt.Fprintf(b, "    now: %s\n", explain(ch.To))
	case profile.KindBetter:
		fmt.Fprintf(b, "    cleared: %s\n", explain(ch.From))
	}
	// KindRemoved gets no cause line: the package is gone after the upgrade, so
	// what it used to fail at cannot block anything.
}

// sideLabel names one side's state in the width of a table column, without
// inventing a verdict for a package that was never judged.
func sideLabel(s profile.Side) string {
	switch {
	case !s.Present:
		return "absent"
	case s.Verdict != "":
		return string(s.Verdict)
	case s.Status != "":
		return string(s.Status)
	}
	return "unknown"
}

// explain turns a side into the one line under a change. For an unjudged
// package the reason is the whole diagnosis; printing "no findings" there would
// claim a clean result that was never obtained.
func explain(s profile.Side) string {
	if !s.Present {
		return "not installed on this host"
	}
	if len(s.Causes) > 0 {
		return strings.Join(s.Causes, ", ")
	}
	if s.Reason != "" {
		return s.Reason
	}
	return "no findings"
}

func changesOfKind(changes []profile.Change, kind profile.ChangeKind) []profile.Change {
	var out []profile.Change
	for _, ch := range changes {
		if ch.Kind == kind {
			out = append(out, ch)
		}
	}
	return out
}
