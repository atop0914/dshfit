package semver

import (
	"strings"
	"testing"
)

// The ranges below are taken from real peerDependencies in the DSH ecosystem
// plus the shapes the parser is most likely to get wrong. They are seeds, not a
// test matrix: the point of a fuzz target is the inputs nobody wrote down.
//
// The MaxInt caret is here on purpose. Expanding it needs m+1, and m+1 does not
// exist for the largest int; that is exactly the kind of branch that only
// reachable-by-accident input hits.
var rangeSeeds = []struct{ raw, host string }{
	{"^0.2.0-rc.1", "0.2.2-rc.1"},
	{">=0.2.0-rc.1 <0.3.0", "0.2.2-rc.1"},
	{"^0.4.0", "0.4.8"},
	{"^0.1.0-rc.6", "0.2.0-rc.2"},
	{"^0.1.0-rc.6 || ^0.2.0-rc.1", "0.2.0-rc.2"},
	{"1.2.3 - 2.3.4", "2.0.0"},
	{"~1.2.3-beta.2", "1.2.3-beta.4"},
	{"1.x.2", "1.9.9"},
	{">= 1.2.3", "1.2.4"},
	{"", "0.2.0"},
	{"*", "0.0.1"},
	{"^9223372036854775807.0.0", "9223372036854775807.0.0"},
	{"x", "0.2.0-rc.1"},
}

// fuzzHosts are evaluated for every generated range, not just the one the
// corpus supplies: a range that behaves for 1.2.3 may still panic on a
// prerelease host, because that is the branch that walks the comparators a
// second time.
var fuzzHosts = []Version{
	{},
	{Major: 0, Minor: 2, Patch: 0, Pre: []string{"rc", "1"}},
	{Major: 0, Minor: 2, Patch: 2, Pre: []string{"rc", "1"}},
	{Major: 0, Minor: 2, Patch: 0},
	{Major: 1, Minor: 2, Patch: 3},
}

func hostsFor(extra Version) []Version {
	out := make([]Version, 0, len(fuzzHosts)+1)
	out = append(out, fuzzHosts...)
	return append(out, extra)
}

// assertExplanation holds the properties that must be true of every explanation
// the tool will ever print. They are checked from the fuzz targets because a
// range that only exists in a fuzzer's corpus reaches the same code that writes
// a user's report.
func assertExplanation(t *testing.T, r Range, v Version) Explanation {
	t.Helper()
	exp := r.Explain(v)
	if got := r.Satisfies(v); got != exp.Satisfied {
		t.Fatalf("Satisfies(%s) = %v but Explain says %v", v, got, exp.Satisfied)
	}
	if exp.Satisfied {
		if exp.Kind != "" {
			t.Fatalf("a satisfied explanation carries Kind %q", exp.Kind)
		}
		return exp
	}
	// An empty Kind prints as "reason=" and an empty Detail prints as nothing,
	// and a rejection nobody can explain is worse than no report: the reader
	// cannot tell it apart from a bug in the tool.
	if exp.Kind != KindOutOfRange && exp.Kind != KindPreReleaseGate {
		t.Fatalf("rejection of %s has Kind %q; want %q or %q", v, exp.Kind, KindOutOfRange, KindPreReleaseGate)
	}
	if strings.TrimSpace(exp.Clause) == "" {
		t.Fatalf("rejection of %s names no clause", v)
	}
	if strings.TrimSpace(exp.Detail) == "" {
		t.Fatalf("rejection of %s has no detail", v)
	}
	return exp
}

// FuzzParseRange asks one question of arbitrary bytes: can a package author's
// peerDependencies string take the whole run down?
//
// It runs before every registry lookup and every profile scan, over text nobody
// on this project wrote. A panic there is not a wrong verdict for one plugin, it
// is the other nineteen plugins losing their verdicts too.
func FuzzParseRange(f *testing.F) {
	for _, s := range rangeSeeds {
		f.Add(s.raw, s.host)
	}
	f.Fuzz(func(t *testing.T, raw, hostRaw string) {
		r, err := ParseRange(raw)
		if err != nil {
			// A failed parse must not leave a usable-looking range behind:
			// callers that ignore the error would otherwise get verdicts from
			// half a range.
			if len(r.sets) != 0 {
				t.Fatalf("ParseRange(%q) failed but returned %d sets", raw, len(r.sets))
			}
			return
		}
		if len(r.sets) == 0 {
			t.Fatalf("ParseRange(%q) succeeded with no clauses", raw)
		}
		host, herr := Parse(hostRaw)
		if herr != nil {
			host = Version{}
		}
		for _, h := range hostsFor(host) {
			assertExplanation(t, r, h)
		}
	})
}

// FuzzExplain asks whether the answer is *usable*, not merely non-crashing.
//
// The thing a reader acts on is the clause and the reason. A rejection with an
// empty Kind or a Detail that never names the version is the tool refusing to
// say why, while still claiming a verdict.
func FuzzExplain(f *testing.F) {
	for _, s := range rangeSeeds {
		f.Add(s.raw, s.host)
	}
	f.Fuzz(func(t *testing.T, raw, hostRaw string) {
		r, err := ParseRange(raw)
		if err != nil {
			return
		}
		host, herr := Parse(hostRaw)
		if herr != nil {
			return
		}
		exp := assertExplanation(t, r, host)
		if exp.Satisfied {
			return
		}
		if !strings.Contains(exp.Detail, host.String()) {
			t.Fatalf("detail %q never names the version it is about (%s)", exp.Detail, host)
		}
		// Two runs over the same pair must produce the same report. Struct
		// equality is enough because Explanation holds no slices.
		if again := r.Explain(host); again != exp {
			t.Fatalf("Explain(%s) is not reproducible: %+v then %+v", host, exp, again)
		}
	})
}
