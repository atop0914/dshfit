package profile

import (
	"fmt"
	"io"
	"strings"
)

// Markdown renders one run as a GitHub-flavoured summary, sized for a CI step
// summary or a PR body rather than a terminal.
//
// It is a second renderer over the same Result, not a second opinion: nothing
// here re-judges a range or recounts a package. Every number comes from Counts
// and every cause comes from Groups, so the markdown and the text output cannot
// disagree about who will drop.
//
// The shape is deliberately four blocks, in the order a reader needs them:
// what happens (headline), why (root causes with the smallest edit), what could
// not be read (unknown, never folded into either), and what was verified clean.
// A summary that only prints problems leaves "checked and fine" and "never
// looked at" indistinguishable, which is the one confusion this tool exists to
// remove.
func Markdown(w io.Writer, res Result) error {
	var b strings.Builder
	writeMarkdown(&b, res)
	// The error is returned rather than ignored: a step summary truncated
	// halfway still looks like a summary, and a CI job that reports "2 blocked"
	// with the fix table cut off is worse than one that fails loudly.
	_, err := io.WriteString(w, b.String())
	return err
}

func writeMarkdown(b *strings.Builder, res Result) {
	c := res.Counts

	fmt.Fprintf(b, "### dshfit · host %s\n\n", codeSpan(res.Host))
	fmt.Fprintf(b, "**%s**\n\n", headline(res))
	fmt.Fprintf(b, "%d package(s): %d ok, %d warn, %d blocked, %d unknown, %d not a plugin",
		c.Packages, c.OK, c.Warn, c.Blocked, c.Unknown, c.NotAPlugin)
	if len(res.Projects) > 0 {
		fmt.Fprintf(b, " — %s", joinCode(res.Projects))
	}
	b.WriteString("\n\n")

	blocked, warn := SplitBySeverity(res.Groups)
	if len(blocked) > 0 {
		fmt.Fprintf(b, "#### Blocked — %d cause(s), %d plugin(s)\n\n",
			len(blocked), DistinctPluginCount(blocked))
		writeGroupTable(b, blocked)
	}
	if len(warn) > 0 {
		fmt.Fprintf(b, "#### Warnings — %d cause(s), %d plugin(s)\n\n",
			len(warn), DistinctPluginCount(warn))
		writeGroupTable(b, warn)
	}
	writeFixNotes(b, append(append([]Group{}, blocked...), warn...))

	if unknown := res.EntriesWithStatus(StatusUnknown); len(unknown) > 0 {
		fmt.Fprintf(b, "#### Could not be judged — %d package(s)\n\n", len(unknown))
		b.WriteString("| package | version | reason |\n|---|---|---|\n")
		for _, e := range unknown {
			fmt.Fprintf(b, "| %s | %s | %s |\n",
				codeSpan(e.Name), codeSpan(e.Version), sanitizeCell(e.Reason))
		}
		// The sentence matters more than the table: an unread package is the
		// outcome that must never be read as compatible.
		b.WriteString("\nThese are not compatible — they are unread. An unread package is the one")
		b.WriteString(" result this run must not report as fine.\n\n")
	}
	if clean := res.CleanPlugins(); len(clean) > 0 {
		fmt.Fprintf(b, "#### No findings — %d plugin(s)\n\n%s\n\n",
			len(clean), joinEntries(clean))
	}
	if notPlugins := res.EntriesWithStatus(StatusNotAPlugin); len(notPlugins) > 0 {
		fmt.Fprintf(b, "%d package(s) declare no `dsh.bundle.patch` and are not plugins: %s\n\n",
			len(notPlugins), joinEntries(notPlugins))
	}
}

// headline is the one sentence a reader who stops after one line will act on.
//
// Two cases exist so that neither can lie. When nothing is blocked and nothing
// is unread, no sentence here may predict a break — that is the reverse
// assertion this project keeps failing tools on. When something is unread,
// "all pass" is forbidden even if nothing is known to be blocked, because the
// two facts are not the same fact.
func headline(res Result) string {
	c := res.Counts
	host := codeSpan(res.Host)
	switch {
	case c.Blocked > 0 && c.Unknown > 0:
		return fmt.Sprintf("%d of %d plugin(s) will not load on %s, and %d package(s) could not be judged.",
			c.Blocked, c.Plugins, host, c.Unknown)
	case c.Blocked > 0:
		return fmt.Sprintf("%d of %d plugin(s) will not load on %s.", c.Blocked, c.Plugins, host)
	case c.Unknown > 0:
		// Nothing is known to break, which is not the same as everything
		// passing: the unknown count is what stops this reading as a clean bill.
		return fmt.Sprintf("Incomplete — nothing is known to break on %s, but %d of %d package(s) could not be judged.",
			host, c.Unknown, c.Packages)
	case len(res.Groups) > 0:
		// Warnings are findings, so "nothing to fix" would be false here; but
		// nothing is predicted to drop, so no break wording either.
		return fmt.Sprintf("All %d plugin(s) load on %s, with %d warning(s).", c.Plugins, host, c.Warn)
	case c.Plugins > 0:
		return fmt.Sprintf("Nothing to fix on %s: all %d plugin(s) load.", host, c.Plugins)
	default:
		return fmt.Sprintf("Nothing to fix on %s: this listing contains no plugin.", host)
	}
}

func writeGroupTable(b *strings.Builder, groups []Group) {
	b.WriteString("| cause | range | plugins | rejects | smallest fix |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, g := range groups {
		cause := codeSpan(g.Code)
		if g.Reason != "" {
			cause += " (" + codeSpan(g.Reason) + ")"
		}
		fmt.Fprintf(b, "| %s | %s | %s | %d | %s |\n",
			cause, codeSpan(g.Range), joinCode(g.Plugins), len(g.Targets), fixCell(g))
	}
	b.WriteString("\n")
}

// writeFixNotes prints what each candidate promises, under the table.
//
// The range alone is the most misread part of this tool's output: `|| >=0.3.0`
// looks like it solves the problem and actually claims every future release.
// The note is what makes the difference sayable, so it travels with the range.
func writeFixNotes(b *strings.Builder, groups []Group) {
	var lines []string
	for _, g := range groups {
		if len(g.FixCandidates) == 0 || g.FixCandidates[0].Note == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s — %s",
			codeSpan(g.FixCandidates[0].Range), sanitizeCell(g.FixCandidates[0].Note)))
	}
	if len(lines) == 0 {
		return
	}
	b.WriteString("What the smallest fix promises:\n\n")
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n")
}

func fixCell(g Group) string {
	if len(g.FixCandidates) > 0 {
		return codeSpan(g.FixCandidates[0].Range)
	}
	if g.FixRefusal != "" {
		return "none — " + sanitizeCell(g.FixRefusal)
	}
	return sanitizeCell(g.Fix)
}

// flatten collapses anything that would end a markdown table cell. Reasons
// embed os errors, which carry newlines and sometimes tabs; a newline inside a
// cell splits the table and silently drops every row after it. Lines are joined
// with one space rather than by deleting the break, so a path broken across
// lines stays readable instead of running two words together.
func flatten(s string) string {
	if s == "" {
		return ""
	}
	if !strings.ContainsAny(s, "\r\n\t") {
		return strings.TrimSpace(s)
	}
	lines := strings.FieldsFunc(s, func(r rune) bool { return r == '\r' || r == '\n' })
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.ReplaceAll(strings.TrimSpace(line), "\t", " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, " ")
}

// sanitizeCell escapes prose for a table cell. `|` would end the cell and `<`
// would let a package.json — third-party input — inject HTML into a rendered
// step summary.
func sanitizeCell(s string) string {
	s = flatten(s)
	if s == "" {
		return "—"
	}
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}

// codeSpan wraps a value that came from someone else's package.json.
//
// Values are put in code spans rather than escaped: a code span carries no
// markdown, so a package named "a`b" cannot close the section or turn into a
// link, and a raw tag inside one is not executed. The pipe is the exception —
// GitHub tables end a cell at `|` even inside a code span, so it is escaped on
// top. CommonMark allows a span delimited by n+1 backticks to contain runs of n
// backticks, as long as a space pads any backtick touching the delimiter.
func codeSpan(s string) string {
	s = flatten(s)
	if s == "" {
		return "—"
	}
	run := longestBacktickRun(s)
	s = strings.ReplaceAll(s, "|", `\|`)
	if run == 0 {
		return "`" + s + "`"
	}
	delim := strings.Repeat("`", run+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		s = " " + s + " "
	}
	return delim + s + delim
}

func longestBacktickRun(s string) int {
	longest, cur := 0, 0
	for _, r := range s {
		if r == '`' {
			cur++
			if cur > longest {
				longest = cur
			}
			continue
		}
		cur = 0
	}
	return longest
}

func joinCode(values []string) string {
	if len(values) == 0 {
		return "—"
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, codeSpan(v))
	}
	return strings.Join(out, ", ")
}

func joinEntries(entries []Entry) string {
	values := make([]string, 0, len(entries))
	for _, e := range entries {
		v := e.Name
		if e.Version != "" {
			v += "@" + e.Version
		}
		values = append(values, v)
	}
	return joinCode(values)
}
