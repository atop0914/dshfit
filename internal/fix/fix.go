// Package fix turns a range that rejected the host into concrete rewrites, and
// makes the same range engine approve each rewrite before it is shown.
//
// The order of those two steps is the whole point. Generating suggestions is
// easy — anyone can type "|| >=" — but a suggestion that was never re-checked is
// worse than no suggestion at all: the user applies it, still cannot install,
// and learns not to trust the tool. So every candidate here is re-parsed and
// re-tested against the host by the same semver package that produced the
// original rejection. Two range implementations would eventually disagree; there
// is exactly one in this program.
//
// What a candidate proves is narrow, and the wording keeps it that way: npm will
// admit the host through this range. It says nothing about whether the plugin
// runs on that host, and nothing about versions that do not exist yet.
package fix

import (
	"errors"
	"fmt"
	"strings"

	"github.com/atop0914/dshfit/internal/semver"
)

// Shape names a rewrite and, with it, the promise the rewrite makes. It is part
// of the JSON contract because two candidates can both admit the host while
// promising very different things about every version after it.
type Shape string

const (
	// ShapeUnionGTE appends "|| >=host": keep what you declare, add the host
	// and everything after it.
	ShapeUnionGTE Shape = "union->="
	// ShapeUnionCaret appends "|| ^host": keep what you declare, add the host's
	// own line.
	ShapeUnionCaret Shape = "union-caret"
	// ShapeUnionExact appends "|| host": keep what you declare, add exactly
	// this version.
	ShapeUnionExact Shape = "union-exact"
	// ShapeReplaceGTE drops the declared range and keeps only ">=host".
	ShapeReplaceGTE Shape = "replace->="
	// ShapeUnionTagless is generated only so it can be refused. It appends a
	// clause that names the host's core without the prerelease tag — the exact
	// shape this project exists to explain — and verification drops it.
	//
	// It stays in the generator on purpose: that is what turns "every candidate
	// was checked" from a claim into an assertion. Delete the check and this
	// range appears in the output as advice that does not work.
	ShapeUnionTagless Shape = "union-tagless"
)

// Candidate is one rewrite that the range engine has accepted.
type Candidate struct {
	Range string `json:"range"`
	Shape Shape  `json:"shape"`
	// Note states what the rewrite promises beyond admitting the host. It talks
	// about the promise and never about compatibility: the engine can prove npm
	// admits a version, not that the plugin runs on it.
	Note string `json:"note"`
	// Verified is the result of re-parsing Range and asking the engine whether
	// the host now passes. It is computed per candidate rather than assumed, so
	// a range that was never checked cannot carry a true here by accident.
	Verified bool `json:"verified"`
}

// Attempt is a shape that was generated and then refused, with the engine's own
// reason. It is evidence, not advice: the text output prints these under
// "not a fix" so the tagless trap is explained instead of hidden.
type Attempt struct {
	Range string `json:"range"`
	Shape Shape  `json:"shape"`
	Why   string `json:"why"`
}

// Result is the solver's answer for one rejected range.
type Result struct {
	Host  string `json:"host"`
	Range string `json:"range"`
	// Kind is the rejection kind the engine reported, carried through so a
	// reader can tell a numeric shortfall from the prerelease gate without
	// re-deriving it.
	Kind string `json:"kind"`
	// Clause is the expanded clause that rejected the host, e.g.
	// ">=0.1.0-rc.6 <0.2.0" for "^0.1.0-rc.6". It is the diagnosis the
	// candidates are meant to repair.
	Clause string `json:"clause"`
	// Detail is the engine's own sentence about the rejection.
	Detail string `json:"detail"`
	// Candidates are self-verified and ordered best first: least disruption to
	// the author's declared intent first, most drastic last.
	Candidates []Candidate `json:"candidates"`
	// Tried holds the shapes verification refused. It is normally empty for a
	// release host and never empty for a prerelease host, where the tagless
	// shape is always generated and always rejected.
	Tried []Attempt `json:"tried,omitempty"`
}

// ErrNotRejected reports a range that already admits the host. Solving it would
// mean proposing an edit to a range that works.
var ErrNotRejected = errors.New("the range already admits the host")

// Solve finds rewrites of raw that admit host.
//
// raw has to genuinely reject host. That case returns ErrNotRejected instead of
// an empty result, because "nothing to fix" and "I could not find anything" must
// not look the same to a caller.
func Solve(hostStr, raw string) (Result, error) {
	host, err := semver.Parse(hostStr)
	if err != nil {
		return Result{}, fmt.Errorf("host version %q: %w", hostStr, err)
	}
	rng, err := semver.ParseRange(raw)
	if err != nil {
		return Result{}, fmt.Errorf("range %q cannot be parsed, so there is no bound to widen: %w", raw, err)
	}
	exp := rng.Explain(host)
	if exp.Satisfied {
		return Result{}, fmt.Errorf("%w: %s satisfies %q", ErrNotRejected, host, raw)
	}

	res := Result{
		Host:   host.String(),
		Range:  raw,
		Kind:   string(exp.Kind),
		Clause: exp.Clause,
		Detail: exp.Detail,
	}
	for _, c := range shapes(raw, host, exp.Kind) {
		if why, ok := verify(c.Range, host); !ok {
			res.Tried = append(res.Tried, Attempt{Range: c.Range, Shape: c.Shape, Why: why})
			continue
		}
		c.Verified = true
		res.Candidates = append(res.Candidates, c)
	}
	return res, nil
}

// shapes generates the rewrites worth trying for one rejection, in the order a
// reader should consider them.
//
// The kind decides what is offered, not just what is said. A prerelease gate
// means the declared numbers were already right and only a name was missing, so
// throwing that range away would trade a nameable defect for an unnamed one and
// is not offered at all. A numeric shortfall is different: dropping the old
// range is a real (if drastic) option, so it is offered last.
func shapes(raw string, host semver.Version, kind semver.Kind) []Candidate {
	base := strings.TrimSpace(raw)
	h := host.String()
	var out []Candidate
	if base != "" {
		out = append(out,
			Candidate{Range: base + " || >=" + h, Shape: ShapeUnionGTE, Note: unionNote(kind, h)},
			Candidate{Range: base + " || ^" + h, Shape: ShapeUnionCaret, Note: caretNote(h)},
			Candidate{Range: base + " || " + h, Shape: ShapeUnionExact, Note: exactNote(h)},
		)
		if host.HasPreRelease() {
			// Note is empty on purpose: this candidate is expected to fail, and
			// a note describing a promise it cannot keep would be the only text
			// ever written about a range nobody should use.
			out = append(out, Candidate{Range: base + " || >=" + host.Core(), Shape: ShapeUnionTagless})
		}
	}
	if kind == semver.KindOutOfRange || base == "" {
		out = append(out, Candidate{Range: ">=" + h, Shape: ShapeReplaceGTE, Note: replaceNote(h)})
	}
	return out
}

func unionNote(kind semver.Kind, host string) string {
	if kind == semver.KindPreReleaseGate {
		return fmt.Sprintf("the added clause repeats %s with its prerelease tag, which is what opens npm's "+
			"gate; a clause without that tag is refused even though the numbers pass", host)
	}
	return fmt.Sprintf("keeps every version the current range admits and adds %s onward; that also claims "+
		"every later release, not just this one", host)
}

func caretNote(host string) string {
	return fmt.Sprintf("claims ^%s — the host's own line only, instead of every release after it", host)
}

func exactNote(host string) string {
	return fmt.Sprintf("pins exactly %s: this release and nothing after it, so the next one needs another edit",
		host)
}

func replaceNote(host string) string {
	return fmt.Sprintf("drops the versions the current range admits and keeps only %s onward; honest only if "+
		"you no longer support them", host)
}

// verify re-parses a candidate and asks the engine whether the host now passes.
// It returns the engine's own rejection detail so a refused shape can be
// explained with the same words the original diagnosis used.
func verify(raw string, host semver.Version) (why string, ok bool) {
	rng, err := semver.ParseRange(raw)
	if err != nil {
		return "the rewritten range does not parse: " + err.Error(), false
	}
	exp := rng.Explain(host)
	if exp.Satisfied {
		return "", true
	}
	return exp.Detail, false
}
