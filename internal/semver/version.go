// Package semver implements the part of node-semver that decides whether a DSH
// plugin can load on a given harness version.
//
// The subset is deliberate. npm ranges carry prefixes, x-ranges, hyphen ranges,
// unions and carets, and npm layers one extra rule on top: a prerelease version
// only satisfies a comparator set if that set contains a comparator naming the
// same major.minor.patch WITH a prerelease tag. Copying node-semver's prefix
// rules without that gate produces a checker that reports "compatible" for
// exactly the upgrades that break in practice.
//
// The concrete case: ^0.1.0-rc.6 expands to >=0.1.0-rc.6 <0.2.0. Numerically
// 0.2.0-rc.2 is below 0.2.0, so a naive comparator says yes. npm says no,
// because no comparator in the set names 0.2.0 with a prerelease tag. That
// difference is why 11 of 22 third-party plugins stopped loading on the day
// 0.2.0-rc.1 shipped.
package semver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed semantic version. Build metadata is carried but never
// affects precedence.
type Version struct {
	Major, Minor, Patch int
	Pre                 []string
	Build               string
}

// HasPreRelease reports whether the version carries a prerelease tag such as
// "-rc.2". This single bit drives the npm gate described in the package doc.
func (v Version) HasPreRelease() bool { return len(v.Pre) > 0 }

// Core renders "major.minor.patch" without prerelease or build parts.
func (v Version) Core() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// SameCore reports whether two versions share major.minor.patch, ignoring
// prerelease tags. The npm prerelease gate is scoped to this triple.
func (v Version) SameCore(o Version) bool {
	return v.Major == o.Major && v.Minor == o.Minor && v.Patch == o.Patch
}

func (v Version) String() string {
	s := v.Core()
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	if v.Build != "" {
		s += "+" + v.Build
	}
	return s
}

// Parse parses a complete version. Partial versions such as "0.2" are rejected
// here on purpose: callers that accept them are parsing a *range*, not a
// version, and must expand the partial first.
func Parse(s string) (Version, error) {
	p, err := parsePartial(s)
	if err != nil {
		return Version{}, err
	}
	if p.wild[0] || p.wild[1] || p.wild[2] {
		return Version{}, fmt.Errorf("version %q is partial or a wildcard", s)
	}
	return p.version(), nil
}

// Compare orders two versions. It returns -1, 0 or 1.
func Compare(a, b Version) int {
	if c := cmpInt(a.Major, b.Major); c != 0 {
		return c
	}
	if c := cmpInt(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := cmpInt(a.Patch, b.Patch); c != 0 {
		return c
	}
	switch {
	case len(a.Pre) == 0 && len(b.Pre) == 0:
		return 0
	case len(a.Pre) == 0:
		return 1 // a release outranks any prerelease of the same core
	case len(b.Pre) == 0:
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		if c := cmpIdentifier(a.Pre[i], b.Pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(a.Pre), len(b.Pre))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpIdentifier orders prerelease identifiers. The spec makes numeric
// identifiers compare numerically and rank below alphanumeric ones, which is
// why 0.2.0-rc.2 < 0.2.0-rc.10 and 0.2.0-2 < 0.2.0-rc.
func cmpIdentifier(a, b string) int {
	an, aErr := strconv.Atoi(a)
	bn, bErr := strconv.Atoi(b)
	aNum, bNum := aErr == nil, bErr == nil
	switch {
	case aNum && bNum:
		return cmpInt(an, bn)
	case aNum:
		return -1
	case bNum:
		return 1
	}
	return strings.Compare(a, b)
}

// partial is a version that may be missing segments or use wildcards. Range
// parsing needs it; version parsing rejects it.
type partial struct {
	nums  [3]int
	wild  [3]bool
	pre   []string
	build string
}

func (p partial) version() Version {
	return Version{
		Major: p.nums[0],
		Minor: p.nums[1],
		Patch: p.nums[2],
		Pre:   p.pre,
		Build: p.build,
	}
}

func parsePartial(s string) (partial, error) {
	var p partial
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" || s == "*" || s == "x" || s == "X" {
		p.wild = [3]bool{true, true, true}
		return p, nil
	}

	if i := strings.IndexByte(s, '+'); i >= 0 {
		p.build = s[i+1:]
		s = s[:i]
	}
	if i := strings.IndexByte(s, '-'); i > 0 {
		pre := s[i+1:]
		if pre == "" {
			return partial{}, fmt.Errorf("empty prerelease in %q", s)
		}
		p.pre = strings.Split(pre, ".")
		s = s[:i]
	}

	segs := strings.Split(s, ".")
	if len(segs) > 3 {
		return partial{}, fmt.Errorf("too many segments in %q", s)
	}
	wildSeen := false
	for i, seg := range segs {
		if seg == "x" || seg == "X" || seg == "*" || seg == "" {
			p.wild[i] = true
			wildSeen = true
			continue
		}
		if wildSeen {
			// npm reads "1.x.2" as "1.x"; a concrete segment after a wildcard
			// cannot constrain anything, so treating it as a wildcard is the
			// only reading that does not invent a constraint.
			p.wild[i] = true
			continue
		}
		n, err := strconv.Atoi(seg)
		if err != nil || n < 0 {
			return partial{}, fmt.Errorf("bad numeric segment %q in %q", seg, s)
		}
		p.nums[i] = n
	}
	for i := len(segs); i < 3; i++ {
		p.wild[i] = true
	}
	if p.pre != nil && (p.wild[1] || p.wild[2]) {
		return partial{}, fmt.Errorf("prerelease on a partial version %q", s)
	}
	return p, nil
}
