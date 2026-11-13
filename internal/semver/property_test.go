package semver

import (
	"strings"
	"testing"
)

// propertyRanges is every shape the parser claims to support, plus the ranges
// that actually appear in the DSH ecosystem. The properties below are checked
// against all of them, so a new prefix rule cannot be added without expanding
// into a range that contains its own bounds.
var propertyRanges = []string{
	"^0.2.0-rc.1",
	">=0.2.0-rc.1 <0.3.0",
	"^0.4.0",
	"^0.1.0-rc.6",
	"^0.0.3",
	"~1.2.3",
	"~1.2.3-beta.2",
	"~1",
	"1.2.3 - 2.3.4",
	"1.2.3 - 2.3",
	">= 1.2.3",
	">1.2.3",
	"<=1.2.3",
	"1.x",
	"1.2.x",
	"1.x.2",
	"*",
	"",
	">=0.2.0",
	// The edge of the int domain. Expanding a caret needs major+1, and for the
	// largest int that number does not exist; whatever the code does about it,
	// it may not produce a range that excludes its own lower bound.
	"^9223372036854775807.0.0",
	// The same edge on every other branch that computes a bound: a wildcard
	// behind ">", "<= 1" (which expands to "< 2.0.0"), a tilde, and a hyphen
	// range. They are separate code paths, so they are checked separately.
	">9223372036854775807",
	"<=9223372036854775807",
	"9223372036854775807.x",
	"~9223372036854775807",
	"9223372036854775807.0.0 - 9223372036854775807.5.0",
}

// TestPropertyLowerBoundSatisfies is the property that catches a wrong
// expansion: if `>=0.2.0-rc.1` does not admit 0.2.0-rc.1, the lower bound was
// computed from something other than the text the author wrote.
//
// A `>` bound is the mirror image and is asserted as such: no range contains the
// version named by its own strict bound, for the same reason.
func TestPropertyLowerBoundSatisfies(t *testing.T) {
	for _, raw := range propertyRanges {
		r, err := ParseRange(raw)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", raw, err)
		}
		for i, set := range r.sets {
			if len(set.comps) == 0 {
				continue // "*" and "": no bound to check
			}
			lo := set.comps[0]
			switch lo.op {
			case opGTE, opEQ:
				if !r.Satisfies(lo.ver) {
					t.Errorf("%q: lower bound %s of clause %d is not in the range (clause %s)",
						raw, lo.ver, i, set.String())
				}
			case opGT:
				if r.Satisfies(lo.ver) {
					t.Errorf("%q: strict lower bound %s is wrongly inside clause %d", raw, lo.ver, i)
				}
			}
		}
	}
}

// ceilingOf states, per range, the version npm's semantics put just outside the
// upper end — the first release the author did not mean to cover.
//
// It is written out here rather than read back from the comparator list, and the
// reason is a measured one: the first version of this test derived the bound from
// the parsed comparators, so flipping `<` to `<=` changed the expectation along
// with the code and left the test green. A property that asks the code what to
// expect cannot notice a change in how the code writes the bound.
var ceilingOf = map[string]string{
	"^0.1.0-rc.6":         "0.2.0",
	"^0.2.0-rc.1":         "0.3.0",
	"^0.4.0":              "1.0.0",
	"^0.0.3":              "0.0.4",
	"~1.2.3":              "1.3.0",
	"~1.2.3-beta.2":       "1.3.0",
	"~1":                  "2.0.0",
	">=0.2.0-rc.1 <0.3.0": "0.3.0",
	"1.2.3 - 2.3.4":       "2.3.5",
	"1.2.3 - 2.3":         "2.4.0",
	"1.x":                 "2.0.0",
	"1.2.x":               "1.3.0",
	"1.x.2":               "2.0.0",
}

// TestPropertyUpperBoundIsExcluded pins the other half of the expansion. The
// upstream breakage is a range whose upper bound is one minor too high, so the
// assertion is that the version just past the end is rejected, not that some
// version far above it is.
func TestPropertyUpperBoundIsExcluded(t *testing.T) {
	for raw, ceiling := range ceilingOf {
		r, err := ParseRange(raw)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", raw, err)
		}
		v, err := Parse(ceiling)
		if err != nil {
			t.Fatalf("Parse(%q): %v", ceiling, err)
		}
		if r.Satisfies(v) {
			t.Errorf("%q admits %s, which is past its own upper end", raw, ceiling)
		}
	}

	// The same claim derived from the expansion, for the ranges no table entry
	// covers. This half also checks the opposite direction: an inclusive upper
	// bound must admit the version it names, or a hyphen range would silently
	// lose its last patch release.
	for _, raw := range propertyRanges {
		r, err := ParseRange(raw)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", raw, err)
		}
		for i, set := range r.sets {
			if len(set.comps) == 0 {
				continue
			}
			hi := set.comps[len(set.comps)-1]
			switch hi.op {
			case opLT:
				if r.Satisfies(hi.ver) {
					t.Errorf("%q: upper bound %s is inside clause %d (clause %s)",
						raw, hi.ver, i, set.String())
				}
			case opLTE:
				if !r.Satisfies(hi.ver) {
					t.Errorf("%q: inclusive upper bound %s is outside clause %d (clause %s)",
						raw, hi.ver, i, set.String())
				}
			}
		}
	}
}

// TestPropertyUnionDoesNotStopAtFirstClause is a regression pin for a real bug
// from this project's first day: Explain returned as soon as the first clause
// failed, so `A || B` was judged by A alone and every branch after "||" was
// decoration. The property is stated without knowing which clause matched — if
// any clause admits a version, the range admits it.
func TestPropertyUnionDoesNotStopAtFirstClause(t *testing.T) {
	unions := []string{
		"^0.1.0-rc.6 || ^0.2.0-rc.1",
		"^1.0.0 || ^0.2.0-rc.1",
		"^0.1.0-rc.6||^0.2.0-rc.1",
		"^0.1.0-rc.6 || >=0.2.0-rc.1 <0.3.0 || ^0.4.0",
	}
	for _, raw := range unions {
		r, err := ParseRange(raw)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", raw, err)
		}
		if len(r.sets) < 2 {
			t.Fatalf("%q parsed into %d clauses, so it tests no union", raw, len(r.sets))
		}
		for _, v := range fuzzHosts {
			for i, set := range r.sets {
				ok, _, _ := set.match(v)
				if !ok {
					continue
				}
				if !r.Satisfies(v) {
					t.Errorf("%q: clause %d (%s) admits %s but the range rejects it",
						raw, i, set.String(), v)
				}
			}
		}
	}
}

// TestPropertyExplanationIsReproducible covers the requirement that a report can
// be re-run: two evaluations of one pair must agree, and a rejection must carry
// a reason. Either failure makes the output unquotable in an issue.
func TestPropertyExplanationIsReproducible(t *testing.T) {
	for _, raw := range propertyRanges {
		r, err := ParseRange(raw)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", raw, err)
		}
		for _, v := range fuzzHosts {
			first := r.Explain(v)
			second := r.Explain(v)
			if first != second {
				t.Errorf("%q vs %s: Explain is not reproducible: %+v then %+v", raw, v, first, second)
			}
			if first.Satisfied != r.Satisfies(v) {
				t.Errorf("%q vs %s: Satisfies and Explain disagree", raw, v)
			}
			if first.Satisfied {
				continue
			}
			if first.Kind != KindOutOfRange && first.Kind != KindPreReleaseGate {
				t.Errorf("%q vs %s: rejection has no usable Kind (%q)", raw, v, first.Kind)
			}
			if strings.TrimSpace(first.Detail) == "" || strings.TrimSpace(first.Clause) == "" {
				t.Errorf("%q vs %s: rejection has no clause or detail: %+v", raw, v, first)
			}
		}
	}
}

// TestPropertyGateIsNotReportedAsOutOfRange is the reverse assertion the project
// cannot ship without: a prerelease that is numerically inside the interval and
// rejected by npm's gate must say "gate", never "out of range". The two readings
// carry opposite instructions — widen the numbers, or name the new core — and
// mixing them up sends the author to the wrong edit.
//
// The last three cases are shapes a fuzzer found no reason to doubt, kept here
// so the classification stays pinned as the parser grows.
func TestPropertyGateIsNotReportedAsOutOfRange(t *testing.T) {
	cases := []struct {
		rng, ver string
		want     Kind
		why      string
	}{
		{"^0.1.0-rc.6", "0.2.0-rc.2", KindPreReleaseGate, "the 0.2.0 breakage itself"},
		{"^0.1.0-rc.6", "0.2.0-rc.9", KindPreReleaseGate, "numerically inside, gate refuses"},
		{"^0.1.0-rc.6", "0.2.0", KindOutOfRange, "a release is never gated"},
		{"~0.1.0-rc.6", "0.2.0-rc.2", KindPreReleaseGate, "tilde widens to the same 0.2 ceiling"},
		{"0.1.x", "0.2.0-rc.2", KindPreReleaseGate, "x-range reaches 0.2.0 numerically"},
		{"1.2.3 - 2.3.4", "2.0.0-rc.1", KindPreReleaseGate, "hyphen range, prerelease core inside"},
		{">=0.2.0-rc.1 <0.3.0", "0.2.5-rc.1", KindPreReleaseGate, "explicit rc lower bound does not admit a sibling core"},
		{">=0.2.0-rc.1 <0.3.0", "0.4.0-rc.1", KindOutOfRange, "outside the numbers, so the numbers are the answer"},
		{"^0.1.0-rc.6", "0.1.0-rc.5", KindOutOfRange, "below the bound is a numeric rejection"},
	}
	for _, c := range cases {
		r, err := ParseRange(c.rng)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", c.rng, err)
		}
		v, err := Parse(c.ver)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.ver, err)
		}
		exp := r.Explain(v)
		if exp.Satisfied {
			t.Errorf("%q admits %s; the case expects a rejection (%s)", c.rng, c.ver, c.why)
			continue
		}
		if exp.Kind != c.want {
			t.Errorf("%q vs %s: Kind = %q, want %q (%s); detail: %s",
				c.rng, c.ver, exp.Kind, c.want, c.why, exp.Detail)
		}
	}

	// The gate is not a blanket ban on prereleases. When a comparator names the
	// same core with a prerelease tag, later tags of that core are admitted;
	// refusing them would be the false positive that makes a compatibility
	// checker worthless, so it is asserted in the same test as the gate itself.
	admitted := []struct{ rng, ver string }{
		{">=0.2.0-rc.1 <0.3.0", "0.2.0-rc.5"},
		{"^0.2.0-rc.1", "0.2.0-rc.5"},
		{"~1.2.3-beta.2", "1.2.3-beta.4"},
	}
	for _, c := range admitted {
		r, err := ParseRange(c.rng)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", c.rng, err)
		}
		v, err := Parse(c.ver)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.ver, err)
		}
		if exp := r.Explain(v); !exp.Satisfied {
			t.Errorf("%q must admit %s (same core, prerelease named by a comparator), got %q: %s",
				c.rng, c.ver, exp.Kind, exp.Detail)
		}
	}
}

// TestExpansionAtTheIntDomainEdge spells out the case the bound properties
// found: a range whose expansion has to compute a number one past the largest
// int. The failure it replaces was a wrong "blocked" (the range excluded its own
// lower bound), so each assertion below is a version that must be admitted —
// except where admitting it would be the opposite mistake.
func TestExpansionAtTheIntDomainEdge(t *testing.T) {
	const max = "9223372036854775807"
	cases := []struct {
		rng, ver string
		want     bool
		why      string
	}{
		{"^" + max + ".0.0", max + ".0.0", true, "the lower bound is always inside"},
		{"^" + max + ".0.0", max + ".9.9", true, "caret 0.x-style ceiling is the next major, which does not exist"},
		{"^" + max + ".0.0", "9223372036854775806.9.9", false, "below the bound is still a rejection"},
		{">" + max, max + ".0.0", false, "'>N' on a wildcard means a major that does not exist, so nothing matches"},
		{">" + max, max + ".9.9", false, "and it is not 'any' either — that would invert the range"},
		{"<=" + max, "0.0.0", true, "'<=N' past the top admits everything below"},
		{"<=" + max, max + ".9.9", true, "including the top itself"},
		{max + ".0.0 - " + max + ".5.0", max + ".5.0", true, "an inclusive hyphen bound is inside"},
		{max + ".0.0 - " + max + ".5.0", max + ".5.1", false, "and one patch above it is not"},
	}
	for _, c := range cases {
		r, err := ParseRange(c.rng)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", c.rng, err)
		}
		v, err := Parse(c.ver)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.ver, err)
		}
		if got := r.Satisfies(v); got != c.want {
			t.Errorf("%q vs %s: Satisfies = %v, want %v (%s)", c.rng, c.ver, got, c.want, c.why)
		}
	}
}

// TestZeroRangeAnswersInsteadOfPanicking keeps the "unreachable" fallthrough in
// Explain honest. A Range that never went through ParseRange has no clauses, and
// the only acceptable behaviour is a rejection with a stated reason — a panic
// here would surface as a crash in a report, and a silent "satisfied" would
// claim compatibility nobody computed.
func TestZeroRangeAnswersInsteadOfPanicking(t *testing.T) {
	var r Range
	v, err := Parse("0.2.2-rc.1")
	if err != nil {
		t.Fatal(err)
	}
	exp := r.Explain(v)
	if exp.Satisfied {
		t.Fatal("an empty Range admitted a version")
	}
	if exp.Kind != KindOutOfRange || exp.Detail == "" {
		t.Fatalf("empty Range explained itself as %+v", exp)
	}
}
