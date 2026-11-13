package semver

import (
	"fmt"
	"math"
	"strings"
)

// Kind classifies why a range rejected a version. The distinction matters:
// "out of range" means the plugin author needs a wider range, while
// "prerelease gate" means the numeric range may already look wide enough and
// only npm's gate is holding the host out. Those need different fixes, and
// telling them apart is the whole point of the explain output.
type Kind string

const (
	KindOutOfRange     Kind = "out-of-range"
	KindPreReleaseGate Kind = "prerelease-gate"
)

// Explanation says whether a version satisfies a range and, when it does not,
// which clause rejected it and why.
type Explanation struct {
	Satisfied bool
	SetIndex  int    // index of the rejecting clause
	Clause    string // source text of that clause
	Kind      Kind
	Detail    string
}

type op int

const (
	opEQ op = iota
	opLT
	opLTE
	opGT
	opGTE
)

func (o op) String() string {
	switch o {
	case opLT:
		return "<"
	case opLTE:
		return "<="
	case opGT:
		return ">"
	case opGTE:
		return ">="
	}
	return "="
}

type comparator struct {
	op  op
	ver Version
}

func (c comparator) String() string { return c.op.String() + c.ver.String() }

func (c comparator) test(v Version) bool {
	switch cmp := Compare(v, c.ver); c.op {
	case opEQ:
		return cmp == 0
	case opLT:
		return cmp < 0
	case opLTE:
		return cmp <= 0
	case opGT:
		return cmp > 0
	case opGTE:
		return cmp >= 0
	}
	return false
}

// comparatorSet is one AND clause of a range. An empty set means "any".
type comparatorSet struct {
	src   string
	comps []comparator
}

func (s comparatorSet) String() string {
	if len(s.comps) == 0 {
		return "*"
	}
	parts := make([]string, len(s.comps))
	for i, c := range s.comps {
		parts[i] = c.String()
	}
	return strings.Join(parts, " ")
}

// match reports whether v satisfies the set. On failure it names the first
// violated comparator, or reports the prerelease gate when every comparator
// passes numerically but npm still refuses the version.
func (s comparatorSet) match(v Version) (bool, comparator, Kind) {
	for _, c := range s.comps {
		if !c.test(v) {
			return false, c, KindOutOfRange
		}
	}
	if !v.HasPreRelease() {
		return true, comparator{}, ""
	}
	for _, c := range s.comps {
		if c.ver.HasPreRelease() && c.ver.SameCore(v) {
			return true, comparator{}, ""
		}
	}
	return false, comparator{}, KindPreReleaseGate
}

// Range is a union of comparator sets, as written in peerDependencies.
type Range struct {
	Raw  string
	sets []comparatorSet
}

// ParseRange parses an npm range: "||" unions, hyphen ranges, x-ranges,
// caret, tilde and plain comparators.
func ParseRange(raw string) (Range, error) {
	r := Range{Raw: raw}
	for _, alt := range strings.Split(raw, "||") {
		set, err := parseSet(alt)
		if err != nil {
			return Range{}, fmt.Errorf("range %q: %w", raw, err)
		}
		r.sets = append(r.sets, set)
	}
	if len(r.sets) == 0 {
		return Range{}, fmt.Errorf("range %q: empty", raw)
	}
	return r, nil
}

func (r Range) Satisfies(v Version) bool { return r.Explain(v).Satisfied }

// Explain evaluates v and, on rejection, reports the clause that explains it
// best.
//
// Every clause must be tried before reporting failure: "||" is a union, so the
// first clause failing says nothing about the range. When all clauses fail, a
// prerelease-gate rejection is preferred over a numeric one, because it is the
// more specific diagnosis (the version was numerically inside, and the gate is
// what refused it) and it points at a different fix.
func (r Range) Explain(v Version) Explanation {
	var first *Explanation
	var gated *Explanation
	for i, set := range r.sets {
		ok, bad, kind := set.match(v)
		if ok {
			return Explanation{Satisfied: true}
		}
		if kind == KindPreReleaseGate {
			if gated == nil {
				gated = &Explanation{
					SetIndex: i,
					Clause:   set.String(),
					Kind:     kind,
					Detail: fmt.Sprintf(
						"%s is a prerelease of %s, and no comparator in %q names %s with a prerelease tag; "+
							"npm only admits prereleases that the range explicitly opts into",
						v, v.Core(), set.String(), v.Core()),
				}
			}
			continue
		}
		if first == nil {
			first = &Explanation{
				SetIndex: i,
				Clause:   set.String(),
				Kind:     kind,
				Detail:   fmt.Sprintf("%s fails comparator %q (clause %q)", v, bad.String(), set.String()),
			}
		}
	}
	if gated != nil {
		return *gated
	}
	if first != nil {
		return *first
	}
	// Unreachable for a parsed range: ParseRange always yields one set.
	return Explanation{Kind: KindOutOfRange, Detail: "range has no clauses"}
}

func parseSet(src string) (comparatorSet, error) {
	trimmed := strings.TrimSpace(src)
	set := comparatorSet{src: trimmed}
	if trimmed == "" {
		return set, nil // "" means any version
	}

	if lo, hi, ok := splitHyphen(trimmed); ok {
		lowPart, err := parsePartial(lo)
		if err != nil {
			return comparatorSet{}, err
		}
		highPart, err := parsePartial(hi)
		if err != nil {
			return comparatorSet{}, err
		}
		set.comps = append(set.comps, comparator{op: opGTE, ver: lowerBound(lowPart)})
		if bound, ok := upperBoundInclusive(highPart); ok {
			set.comps = append(set.comps, comparator{op: opLTE, ver: bound})
		} else if bound, ok := upperBoundExclusive(highPart); ok {
			set.comps = append(set.comps, comparator{op: opLT, ver: bound})
		}
		return set, nil
	}

	fields := strings.Fields(trimmed)
	for i := 0; i < len(fields); i++ {
		tok := fields[i]
		// npm accepts a space between an operator and its operand (">= 1.2.3").
		if isBareOperator(tok) && i+1 < len(fields) {
			tok += fields[i+1]
			i++
		}
		comps, err := expand(tok)
		if err != nil {
			return comparatorSet{}, err
		}
		set.comps = append(set.comps, comps...)
	}
	return set, nil
}

func isBareOperator(s string) bool {
	switch s {
	case "=", ">", ">=", "<", "<=", "^", "~":
		return true
	}
	return false
}

// splitHyphen finds "1.2.3 - 2.3.4". A "-" inside a prerelease tag carries no
// surrounding spaces, so requiring " - " is enough to tell the two apart.
func splitHyphen(s string) (lo, hi string, ok bool) {
	i := strings.Index(s, " - ")
	if i < 0 {
		return "", "", false
	}
	lo = strings.TrimSpace(s[:i])
	hi = strings.TrimSpace(s[i+3:])
	if lo == "" || hi == "" {
		return "", "", false
	}
	if strings.ContainsAny(lo+hi, "<>=^~|") {
		return "", "", false
	}
	return lo, hi, true
}

// Version numbers are ints here, so "one past the largest major" does not exist.
// Caret, tilde, wildcard comparators and hyphen ranges all need to compute a
// bound one step above what the author wrote, and every one of them can be handed
// the largest int. Letting that addition wrap produced a real wrong answer:
// `^9223372036854775807.0.0` expanded to `>=9223372036854775807.0.0
// <-9223372036854775808.0.0`, a range that excludes its own lower bound — a
// plugin that loads fine reported as blocked.
//
// The two helpers below state what a missing bound means instead of computing it.
const maxDimension = math.MaxInt

// topVersion is greater than every other version, so "> topVersion" admits
// nothing: it stands in for a lower bound one past the largest representable
// number, which no version can reach. Returning no comparators instead would be
// read as "any version", i.e. the exact opposite.
var topVersion = Version{Major: maxDimension, Minor: maxDimension, Patch: maxDimension}

// incMajor / incMinor / incPatch return the next bound in that dimension, or
// false when it does not fit. An upper bound that cannot be represented is above
// every version, so "< bound" is true for all of them and the comparator is
// dropped; a lower bound that cannot be represented is unreachable, so it becomes
// a comparison against topVersion.
func incMajor(m int) (Version, bool) {
	if m >= maxDimension {
		return Version{}, false
	}
	return Version{Major: m + 1}, true
}

func incMinor(m, n int) (Version, bool) {
	if n >= maxDimension {
		return Version{}, false
	}
	return Version{Major: m, Minor: n + 1}, true
}

func incPatch(v Version) (Version, bool) {
	if v.Patch >= maxDimension {
		return Version{}, false
	}
	v.Patch++
	return v, true
}

func expand(tok string) ([]comparator, error) {
	s := strings.TrimSpace(tok)
	if s == "" {
		return nil, nil
	}
	prefix := ""
	for _, cand := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(s, cand) {
			prefix = cand
			s = s[len(cand):]
			break
		}
	}
	p, err := parsePartial(s)
	if err != nil {
		return nil, err
	}
	switch prefix {
	case "^":
		return caret(p), nil
	case "~":
		return tilde(p), nil
	}
	return plain(prefix, p), nil
}

func caret(p partial) []comparator {
	if p.wild[0] {
		return nil
	}
	m, n, pt := p.nums[0], p.nums[1], p.nums[2]
	lo := p.version()
	if p.wild[1] {
		lo.Minor, lo.Patch = 0, 0
	}
	if p.wild[2] {
		lo.Patch = 0
	}
	// The upper bound is omitted when it cannot be represented, which means it
	// is above every version — including this range's own lower bound.
	comps := []comparator{{op: opGTE, ver: lo}}
	var hi Version
	var ok bool
	switch {
	case p.wild[1]:
		hi, ok = incMajor(m)
	case p.wild[2]:
		if m > 0 {
			hi, ok = incMajor(m)
		} else {
			hi, ok = incMinor(m, n)
		}
	case m > 0:
		hi, ok = incMajor(m)
	case n > 0:
		hi, ok = incMinor(m, n)
	default:
		hi, ok = incPatch(Version{Major: m, Minor: n, Patch: pt})
	}
	if ok {
		comps = append(comps, comparator{op: opLT, ver: hi})
	}
	return comps
}

func tilde(p partial) []comparator {
	if p.wild[0] {
		return nil
	}
	m, n := p.nums[0], p.nums[1]
	lo := p.version()
	if p.wild[1] {
		lo.Minor, lo.Patch = 0, 0
	}
	if p.wild[2] {
		lo.Patch = 0
	}
	comps := []comparator{{op: opGTE, ver: lo}}
	var hi Version
	var ok bool
	if p.wild[1] {
		hi, ok = incMajor(m)
	} else {
		hi, ok = incMinor(m, n)
	}
	if ok {
		comps = append(comps, comparator{op: opLT, ver: hi})
	}
	return comps
}

func plain(prefix string, p partial) []comparator {
	if p.wild[0] {
		return nil
	}
	m, n := p.nums[0], p.nums[1]
	switch {
	case p.wild[1]: // "1", "1.x"
		switch prefix {
		case ">":
			// "> 1" is ">= 2.0.0"; when 2.0.0 does not exist, nothing matches.
			if hi, ok := incMajor(m); ok {
				return []comparator{{op: opGTE, ver: hi}}
			}
			return []comparator{{op: opGT, ver: topVersion}}
		case ">=":
			return []comparator{{op: opGTE, ver: Version{Major: m}}}
		case "<":
			return []comparator{{op: opLT, ver: Version{Major: m}}}
		case "<=":
			// "<= 1" is "< 2.0.0"; past the top that is every version.
			if hi, ok := incMajor(m); ok {
				return []comparator{{op: opLT, ver: hi}}
			}
			return []comparator{{op: opLTE, ver: topVersion}}
		}
		if hi, ok := incMajor(m); ok {
			return []comparator{
				{op: opGTE, ver: Version{Major: m}},
				{op: opLT, ver: hi},
			}
		}
		return []comparator{{op: opGTE, ver: Version{Major: m}}}
	case p.wild[2]: // "1.2", "1.2.x"
		switch prefix {
		case ">":
			if hi, ok := incMinor(m, n); ok {
				return []comparator{{op: opGTE, ver: hi}}
			}
			return []comparator{{op: opGT, ver: topVersion}}
		case ">=":
			return []comparator{{op: opGTE, ver: Version{Major: m, Minor: n}}}
		case "<":
			return []comparator{{op: opLT, ver: Version{Major: m, Minor: n}}}
		case "<=":
			if hi, ok := incMinor(m, n); ok {
				return []comparator{{op: opLT, ver: hi}}
			}
			return []comparator{{op: opLTE, ver: topVersion}}
		}
		if hi, ok := incMinor(m, n); ok {
			return []comparator{
				{op: opGTE, ver: Version{Major: m, Minor: n}},
				{op: opLT, ver: hi},
			}
		}
		return []comparator{{op: opGTE, ver: Version{Major: m, Minor: n}}}
	}
	v := p.version()
	if prefix == "" || prefix == "=" {
		return []comparator{{op: opEQ, ver: v}}
	}
	o := opGTE
	switch prefix {
	case "<":
		o = opLT
	case "<=":
		o = opLTE
	case ">":
		o = opGT
	}
	return []comparator{{op: o, ver: v}}
}

func lowerBound(p partial) Version {
	v := p.version()
	if p.wild[1] {
		v.Minor, v.Patch = 0, 0
	}
	if p.wild[2] {
		v.Patch = 0
	}
	return v
}

// upperBoundInclusive handles the right side of a hyphen range whose end names a
// full version: "1.2.3 - 2.3.4" includes 2.3.4.
func upperBoundInclusive(p partial) (Version, bool) {
	if p.wild[1] || p.wild[2] {
		return Version{}, false
	}
	return p.version(), true
}

// upperBoundExclusive handles the partial end: "1.2.3 - 2.3" means "up to but not
// including 2.4.0", and "1.2.3 - 2" means "up to 3.0.0".
//
// A false return means that bound lies past the top of the version domain (a
// hyphen range ending at the largest minor), which no version can reach, so the
// caller drops the comparator — the same statement, without inventing a number.
func upperBoundExclusive(p partial) (Version, bool) {
	v := p.version()
	if p.wild[1] {
		return incMajor(v.Major)
	}
	return incMinor(v.Major, v.Minor)
}
