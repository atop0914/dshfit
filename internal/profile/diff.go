package profile

import (
	"sort"
	"strings"

	"github.com/atop0914/dshfit/internal/compat"
)

// ChangeKind says which way one package moved between two hosts.
//
// The three "movement" kinds are separated by what a reader has to do about
// them, not by whether a string differs. That distinction is the whole reason
// this file exists: `blocked -> blocked` looks like no change and is often the
// most important line in the report.
type ChangeKind string

const (
	// KindWorse is a package that loads on the source host and is blocked on
	// the target host. This is the line the user upgraded to find.
	KindWorse ChangeKind = "worse"
	// KindBetter is a package that is blocked on the source host and loads on
	// the target host, or one whose warning went away.
	KindBetter ChangeKind = "better"
	// KindCause is a package whose loadability did not change but whose
	// diagnosis did: a new warning, a warning that went away, or — the case
	// that matters — the same block for the opposite reason. `out-of-range`
	// needs a wider range, `prerelease-gate` needs the range to name the new
	// core explicitly, so reporting "still blocked" without the reason would
	// hand out the wrong edit.
	KindCause ChangeKind = "cause"
	// KindAdded is a package that is only in the target listing.
	KindAdded ChangeKind = "added"
	// KindRemoved is a package that is only in the source listing.
	KindRemoved ChangeKind = "removed"
)

// Side is what one host said about one package.
type Side struct {
	// Present is false when that host's listing had no such package at all.
	// It is kept apart from "present but unknown": one means the plugin is not
	// installed there, the other means it is installed and could not be judged.
	Present bool           `json:"present"`
	Status  Status         `json:"status,omitempty"`
	Verdict compat.Verdict `json:"verdict,omitempty"`
	Version string         `json:"version,omitempty"`
	// Causes are the issues behind the verdict as "code" or "code(reason)",
	// sorted and de-duplicated. Empty for ok and for packages not judged.
	Causes []string `json:"causes,omitempty"`
	// Reason explains a package that was not judged. It is carried because
	// "unknown" with no reason is a dead end for the reader.
	Reason string `json:"reason,omitempty"`
}

// Change is one package that differs between the two hosts.
type Change struct {
	Kind ChangeKind `json:"kind"`
	Name string     `json:"name"`
	From Side       `json:"from"`
	To   Side       `json:"to"`
}

// DiffCounts is the summary of one comparison. The five fields partition
// Changes, so their sum is len(Changes).
type DiffCounts struct {
	Worse   int `json:"worse"`
	Better  int `json:"better"`
	Cause   int `json:"cause"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// Total is the number of packages that differ.
func (c DiffCounts) Total() int {
	return c.Worse + c.Better + c.Cause + c.Added + c.Removed
}

// Diff is the answer to "what does upgrading change?".
type Diff struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Projects are the workspace members, as in Result.
	Projects []string `json:"projects,omitempty"`
	// Changes holds only packages that actually differ, sorted by kind and
	// then by name. A package that behaves identically on both hosts is not
	// in here — that is the point of the command.
	Changes []Change   `json:"changes"`
	Counts  DiffCounts `json:"counts"`
	// ToCounts is the target host's own summary. A diff alone would let a
	// reader conclude "nothing changed, so I am safe" while three packages are
	// blocked on both hosts, so the state being moved into travels with it.
	ToCounts Counts `json:"to_counts"`
}

// Compare reports what differs between two assessments of the same listing.
//
// It takes finished Results rather than packages on purpose: this function must
// never judge a range. Two independent judges in one tool diverge eventually,
// and the day they do, "the diff says it will break but the single run says it
// is fine" becomes an argument nobody can settle. The caller runs Assess twice
// and hands both answers here.
//
// The comparison is by package name, not by index: the two entries slices are
// each sorted by name, but sorting is an implementation detail of Assess, and a
// diff that silently pairs entry 3 of one side with entry 3 of the other would
// be wrong the moment either side gains a package.
func Compare(from, to Result) Diff {
	d := Diff{
		From:     from.Host,
		To:       to.Host,
		Projects: to.Projects,
		Changes:  []Change{},
		ToCounts: to.Counts,
	}
	if len(d.Projects) == 0 {
		d.Projects = from.Projects
	}
	left := indexByName(from.Entries)
	right := indexByName(to.Entries)
	for _, name := range unionNames(left, right) {
		ea, inLeft := left[name]
		eb, inRight := right[name]
		a, b := sideOf(ea, inLeft), sideOf(eb, inRight)
		kind, changed := classify(a, b)
		if !changed {
			continue
		}
		d.Changes = append(d.Changes, Change{Kind: kind, Name: name, From: a, To: b})
	}
	sort.SliceStable(d.Changes, func(i, j int) bool {
		ki, kj := kindRank(d.Changes[i].Kind), kindRank(d.Changes[j].Kind)
		if ki != kj {
			return ki < kj
		}
		return d.Changes[i].Name < d.Changes[j].Name
	})
	d.Counts = countChanges(d.Changes)
	return d
}

// classify decides whether a pair of sides is a reportable change.
//
// The first split is "does it load", because that is the question the user is
// asking. Only when loadability is unchanged does the diagnosis get to decide,
// which is what makes a blocked->blocked reason change a distinct kind instead
// of being swallowed by an equal verdict string.
func classify(a, b Side) (ChangeKind, bool) {
	if !a.Present {
		return KindAdded, true
	}
	if !b.Present {
		return KindRemoved, true
	}
	aBlocked := a.Verdict == compat.Blocked
	bBlocked := b.Verdict == compat.Blocked
	switch {
	case !aBlocked && bBlocked:
		return KindWorse, true
	case aBlocked && !bBlocked:
		return KindBetter, true
	}
	if signature(a) == signature(b) {
		return "", false
	}
	return KindCause, true
}

// signature is everything that makes two sides "the same diagnosis".
//
// Verdict is in it so a verdict change can never pass unnoticed even if the
// issue list were to stay identical; status is in it so a package that stopped
// being a plugin is not read as "ok on both sides"; and the reason of an
// unjudged package is in it because for those entries the reason *is* the
// diagnosis, with no issue list to fall back on.
//
// Version is deliberately *not* in it. A version bump that changes neither the
// verdict nor the causes is not something this command has an opinion about,
// and printing it would train the reader to skim the sections that matter.
func signature(s Side) string {
	var b strings.Builder
	b.WriteString(string(s.Status))
	b.WriteByte(0)
	b.WriteString(string(s.Verdict))
	for _, c := range s.Causes {
		b.WriteByte(0)
		b.WriteString(c)
	}
	if s.Status != StatusPlugin {
		b.WriteByte(0)
		b.WriteString(s.Reason)
	}
	return b.String()
}

func sideOf(e Entry, present bool) Side {
	if !present {
		return Side{}
	}
	s := Side{Present: true, Status: e.Status, Version: e.Version, Reason: e.Reason}
	if e.Report != nil {
		s.Verdict = e.Report.Verdict
		s.Causes = causesOf(e.Report.Issues)
	}
	return s
}

// causesOf renders issues as stable labels. The reason rides along in
// parentheses because the code alone cannot tell the two rejection kinds apart,
// and those two need opposite fixes.
func causesOf(issues []compat.Issue) []string {
	if len(issues) == 0 {
		return nil
	}
	set := make(map[string]bool, len(issues))
	for _, is := range issues {
		label := is.Code
		if is.Reason != "" {
			label += "(" + is.Reason + ")"
		}
		set[label] = true
	}
	return sortedSet(set)
}

func indexByName(entries []Entry) map[string]Entry {
	idx := make(map[string]Entry, len(entries))
	for _, e := range entries {
		idx[e.Name] = e
	}
	return idx
}

func unionNames(left, right map[string]Entry) []string {
	names := make([]string, 0, len(left)+len(right))
	for name := range left {
		names = append(names, name)
	}
	for name := range right {
		if _, dup := left[name]; !dup {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func countChanges(changes []Change) DiffCounts {
	var c DiffCounts
	for _, ch := range changes {
		switch ch.Kind {
		case KindWorse:
			c.Worse++
		case KindBetter:
			c.Better++
		case KindCause:
			c.Cause++
		case KindAdded:
			c.Added++
		case KindRemoved:
			c.Removed++
		}
	}
	return c
}

// kindRank orders the sections a reader wants first: what breaks, what heals,
// what changed quietly, then what merely appeared or vanished.
func kindRank(k ChangeKind) int {
	switch k {
	case KindWorse:
		return 0
	case KindBetter:
		return 1
	case KindCause:
		return 2
	case KindAdded:
		return 3
	case KindRemoved:
		return 4
	}
	return 5
}
