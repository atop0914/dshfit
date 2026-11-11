package profile

import "github.com/atop0914/dshfit/internal/compat"

// Small queries over a Result, in one place so the text, markdown and JSON
// answers cannot drift apart. Each of them has a definition that matters — what
// counts as clean, what counts as distinct — and two copies of a definition is
// two answers waiting to disagree.

// EntriesWithStatus returns the entries carrying one status, in listing order.
func (r Result) EntriesWithStatus(s Status) []Entry {
	var out []Entry
	for _, e := range r.Entries {
		if e.Status == s {
			out = append(out, e)
		}
	}
	return out
}

// CleanPlugins returns judged plugins with the ok verdict. A warning is not
// clean: engines.dsh is never enforced, but it is the author's stated window,
// and hiding it would turn "I checked" into "I looked at the range only".
func (r Result) CleanPlugins() []Entry {
	var out []Entry
	for _, e := range r.Entries {
		if e.Status == StatusPlugin && e.Report != nil && e.Report.Verdict == compat.OK {
			out = append(out, e)
		}
	}
	return out
}

// DistinctPluginCount counts the plugins a set of groups names, each once. One
// plugin can appear under three causes, so the count is not a sum of lengths.
func DistinctPluginCount(groups []Group) int {
	seen := map[string]bool{}
	for _, g := range groups {
		for _, p := range g.Plugins {
			seen[p] = true
		}
	}
	return len(seen)
}

// SplitBySeverity separates the causes that stop a load from the ones that only
// warn. The two are printed under different headings because they call for
// different actions — a warning is the author's stated window, not a break.
func SplitBySeverity(groups []Group) (blocked, warn []Group) {
	for _, g := range groups {
		if g.Severity == compat.Block {
			blocked = append(blocked, g)
			continue
		}
		warn = append(warn, g)
	}
	return blocked, warn
}
