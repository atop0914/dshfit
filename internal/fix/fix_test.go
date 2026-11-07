package fix

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/atop0914/dshfit/internal/semver"
)

// hostVersions is the shortlist of hosts the tool is actually used against: a
// release, the two prereleases of the line that broke the ecosystem, and an
// alpha.
const (
	hostRC1      = "0.2.2-rc.1"
	hostRelease  = "0.2.2"
	hostAlpha    = "0.3.0-alpha.2"
	hostNextLine = "0.3.0"
)

// cases is the table the solver is measured on. Every row must be a range that
// really rejects its host — assertNotRejected enforces that, because a fixture
// that already passes would make the whole row vacuous.
var cases = []struct {
	name string
	host string
	raw  string
	kind semver.Kind
}{
	{"caret on 0.x", hostRC1, "^0.1.0-rc.6", semver.KindOutOfRange},
	{"caret that only lacks the tag", hostRC1, "^0.2.0-rc.1", semver.KindPreReleaseGate},
	{"tilde", hostNextLine, "~0.2.0", semver.KindOutOfRange},
	{"hyphen", "0.3.5", "0.1.0 - 0.2.9", semver.KindOutOfRange},
	{"union", hostRC1, "^0.1.0-rc.6 || ^0.1.9", semver.KindOutOfRange},
	{"release host", hostRelease, "^0.1.0", semver.KindOutOfRange},
	{"alpha host", hostAlpha, "^0.2.0", semver.KindPreReleaseGate},
	{"wildcard against a prerelease", hostRC1, "*", semver.KindPreReleaseGate},
	{"upper bound below a later prerelease", hostRC1, "^0.2.2-rc.2", semver.KindOutOfRange},
}

// assertNotRejected is the reverse assertion every solver test needs: if the
// original range admitted the host there would be nothing to solve, and a
// candidate list would be an answer to a question nobody asked.
func assertNotRejected(t *testing.T, raw, host string) {
	t.Helper()
	h, err := semver.Parse(host)
	if err != nil {
		t.Fatalf("bad fixture host %q: %v", host, err)
	}
	rng, err := semver.ParseRange(raw)
	if err != nil {
		t.Fatalf("bad fixture range %q: %v", raw, err)
	}
	if rng.Satisfies(h) {
		t.Fatalf("fixture is wrong: %q already admits %s", raw, host)
	}
}

// assertCandidateReallyPasses re-checks a candidate without looking at its
// Verified field. The field is what the code claims; this is what the range
// engine says, and only the second one is evidence.
func assertCandidateReallyPasses(t *testing.T, c Candidate, host string) {
	t.Helper()
	h, err := semver.Parse(host)
	if err != nil {
		t.Fatalf("bad host %q: %v", host, err)
	}
	rng, err := semver.ParseRange(c.Range)
	if err != nil {
		t.Fatalf("candidate %q does not parse: %v", c.Range, err)
	}
	if !rng.Satisfies(h) {
		t.Errorf("candidate %q does not admit %s, yet it was returned", c.Range, host)
	}
	if !c.Verified {
		t.Errorf("candidate %q is returned but not marked verified", c.Range)
	}
}

func TestSolveEveryCandidatePassesTheEngine(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertNotRejected(t, tc.raw, tc.host)
			res, err := Solve(tc.host, tc.raw)
			if err != nil {
				t.Fatalf("Solve: %v", err)
			}
			if res.Kind != string(tc.kind) {
				t.Errorf("kind = %q, want %q", res.Kind, tc.kind)
			}
			if len(res.Candidates) == 0 {
				t.Fatalf("no candidates for %q against %s", tc.raw, tc.host)
			}
			for _, c := range res.Candidates {
				assertCandidateReallyPasses(t, c, tc.host)
			}
		})
	}
}

// TestSolveCaretZeroX pins the headline case: ^0.1.0-rc.6 stops at 0.2.0, and the
// smallest honest edit keeps what the author declared.
func TestSolveCaretZeroX(t *testing.T) {
	res, err := Solve(hostRC1, "^0.1.0-rc.6")
	if err != nil {
		t.Fatal(err)
	}
	if res.Clause != ">=0.1.0-rc.6 <0.2.0" {
		t.Errorf("clause = %q, want the expanded caret", res.Clause)
	}
	if got, want := res.Candidates[0].Range, "^0.1.0-rc.6 || >=0.2.2-rc.1"; got != want {
		t.Errorf("best candidate = %q, want %q", got, want)
	}
	if res.Candidates[0].Shape != ShapeUnionGTE {
		t.Errorf("shape = %q", res.Candidates[0].Shape)
	}
	// The note has to admit what the edit promises, or the user reads "works"
	// where the tool only knows "npm admits it".
	if note := res.Candidates[0].Note; !strings.Contains(note, "every later release") {
		t.Errorf("the note should say the edit claims later releases too: %q", note)
	}
	// Here the tagless shape (">=0.2.2") fails *numerically*, because 0.2.2-rc.1
	// sorts below 0.2.2. It is still the shape authors write, so the refusal is
	// recorded with the comparator that refused it.
	if len(res.Tried) != 1 || res.Tried[0].Shape != ShapeUnionTagless {
		t.Fatalf("want exactly one refused tagless attempt, got %+v", res.Tried)
	}
	if !strings.Contains(res.Tried[0].Why, "fails comparator") {
		t.Errorf("a numeric refusal should quote the comparator: %q", res.Tried[0].Why)
	}
}

// TestSolvePrereleaseGateIsNotANumericShortfall is the separation this project
// exists for: the numbers already pass, so the fix must name the core with its
// tag, and the tagless shape must be refused rather than offered.
func TestSolvePrereleaseGateIsNotANumericShortfall(t *testing.T) {
	res, err := Solve(hostRC1, "^0.2.0-rc.1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != string(semver.KindPreReleaseGate) {
		t.Fatalf("kind = %q, want prerelease-gate", res.Kind)
	}
	if got := res.Candidates[0].Range; got != "^0.2.0-rc.1 || >=0.2.2-rc.1" {
		t.Errorf("best candidate = %q, want the host named with its tag", got)
	}
	if !strings.Contains(res.Candidates[0].Note, "tag") {
		t.Errorf("the gate note should explain the tag: %q", res.Candidates[0].Note)
	}
	// Dropping the declared range is not advice here: the declared numbers were
	// never the problem.
	for _, c := range res.Candidates {
		if c.Shape == ShapeReplaceGTE {
			t.Errorf("the gate case offered dropping the range: %q", c.Range)
		}
	}
	// The naive shape — appending the release 0.2.2 — is the one every author
	// reaches for, and it must be shown as refused, never as advice.
	tagless := 0
	for _, a := range res.Tried {
		if a.Shape != ShapeUnionTagless {
			continue
		}
		tagless++
		if a.Range != "^0.2.0-rc.1 || >=0.2.2" {
			t.Errorf("unexpected tagless attempt %q", a.Range)
		}
		if a.Why == "" {
			t.Error("a refused attempt must say why, in the engine's words")
		}
	}
	if tagless != 1 {
		t.Fatalf("got %d tagless attempts, want exactly 1 in Tried: %+v", tagless, res.Tried)
	}
	for _, c := range res.Candidates {
		if strings.HasSuffix(c.Range, "|| >=0.2.2") {
			t.Fatalf("a range that does not admit the host was offered as a candidate: %q", c.Range)
		}
	}
}

// TestSolveOutOfRangeOffersDroppingLast: a numeric shortfall does have the
// drastic option, and it belongs last.
func TestSolveOutOfRangeOffersDroppingLast(t *testing.T) {
	res, err := Solve(hostRC1, "^0.1.0-rc.6")
	if err != nil {
		t.Fatal(err)
	}
	last := res.Candidates[len(res.Candidates)-1]
	if last.Shape != ShapeReplaceGTE || last.Range != ">=0.2.2-rc.1" {
		t.Fatalf("last candidate = %+v, want the replace shape", last)
	}
	if !strings.Contains(last.Note, "drops the versions") {
		t.Errorf("the replace note should say what it costs: %q", last.Note)
	}
}

// TestSolveCandidatesAreDistinct: two shapes that produce the same text are one
// candidate, or the list looks longer than the choice it offers.
func TestSolveCandidatesAreDistinct(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Solve(tc.host, tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for _, c := range res.Candidates {
				if seen[c.Range] {
					t.Errorf("duplicate candidate %q", c.Range)
				}
				seen[c.Range] = true
			}
		})
	}
}

// TestSolveRefusesUnparseableRange: with no parseable bound there is no widening
// to compute, and an empty candidate list would read as "nothing needed".
func TestSolveRefusesUnparseableRange(t *testing.T) {
	res, err := Solve(hostRC1, ">=1.2.3.4")
	if err == nil {
		t.Fatalf("Solve returned %d candidates for an unparseable range", len(res.Candidates))
	}
	if len(res.Candidates) != 0 || len(res.Tried) != 0 {
		t.Errorf("a refusal must not carry candidates: %+v", res)
	}
	if !strings.Contains(err.Error(), "cannot be parsed") {
		t.Errorf("error should say the range is unparseable: %v", err)
	}
}

// TestSolveRefusesRangeThatAlreadyWorks keeps the two silences apart: "nothing to
// fix" is an error, not an empty answer that a caller could print as a failure.
func TestSolveRefusesRangeThatAlreadyWorks(t *testing.T) {
	// Note the core: >=0.2.0-rc.1 does *not* admit 0.2.2-rc.1 — different
	// core, so the gate still refuses — which is why the working fixture has to
	// name 0.2.2 itself.
	res, err := Solve(hostRC1, ">=0.2.2-rc.1")
	if !errors.Is(err, ErrNotRejected) {
		t.Fatalf("err = %v, want ErrNotRejected", err)
	}
	if len(res.Candidates) != 0 {
		t.Errorf("candidates = %+v, want none", res.Candidates)
	}
}

// TestSolveTaglessTrapFiresOnEveryPrereleaseHost: the trap is generated from the
// host shape, not from a guess about the range, so an alpha gets it too.
func TestSolveTaglessTrapFiresOnEveryPrereleaseHost(t *testing.T) {
	for _, tc := range []struct{ host, raw string }{
		{hostRC1, "^0.2.0-rc.1"},
		{hostAlpha, "^0.2.0"},
		{hostRC1, "*"},
	} {
		res, err := Solve(tc.host, tc.raw)
		if err != nil {
			t.Fatalf("%s / %s: %v", tc.raw, tc.host, err)
		}
		found := false
		for _, a := range res.Tried {
			if a.Shape == ShapeUnionTagless {
				found = true
			}
		}
		if !found {
			t.Errorf("%s against %s: the tagless shape was never tried, so its refusal is unproven",
				tc.raw, tc.host)
		}
	}
}

// TestSolveIsDeterministic: the same question twice must not look like two
// different answers, or a diff between two runs is noise.
func TestSolveIsDeterministic(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first, err := Solve(tc.host, tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			a, err := json.Marshal(first)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				again, err := Solve(tc.host, tc.raw)
				if err != nil {
					t.Fatal(err)
				}
				b, err := json.Marshal(again)
				if err != nil {
					t.Fatal(err)
				}
				if string(a) != string(b) {
					t.Fatalf("run %d differs:\n%s\n%s", i, a, b)
				}
			}
		})
	}
}

// TestSolveBadHostIsAnError: a host that cannot be parsed means the question
// cannot be asked, which is different from a range that cannot be widened.
func TestSolveBadHostIsAnError(t *testing.T) {
	if _, err := Solve("not-a-version", "^0.1.0"); err == nil {
		t.Fatal("Solve accepted a host version that cannot be parsed")
	}
}
