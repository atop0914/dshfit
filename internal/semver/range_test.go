package semver

import (
	"strings"
	"testing"
)

func TestRangeSatisfies(t *testing.T) {
	cases := []struct {
		name string
		rng  string
		ver  string
		want bool
		kind Kind
	}{
		// The 0.2 breakage, as a regression test. ^0.1.0-rc.6 is a range over
		// 0.1.x by construction, so every 0.2.x prerelease is a prerelease of a
		// core the range never mentions.
		{"ecosystem: caret 0.1 does not reach 0.2 rc", "^0.1.0-rc.6", "0.2.0-rc.2", false, KindPreReleaseGate},
		{"caret 0.1 admits its own rc", "^0.1.0-rc.6", "0.1.0-rc.6", true, ""},
		{"caret 0.1 admits later 0.1 patch", "^0.1.0-rc.6", "0.1.5", true, ""},
		{"caret 0.1 stops at 0.2 release", "^0.1.0-rc.6", "0.2.0", false, KindOutOfRange},
		{"caret 0.1 rejects earlier rc", "^0.1.0-rc.6", "0.1.0-rc.5", false, KindOutOfRange},
		{"caret 0.2 admits its own rc", "^0.2.0-rc.2", "0.2.0-rc.2", true, ""},
		{"caret 0.2 admits the release", "^0.2.0-rc.2", "0.2.0", true, ""},
		{"caret 0.2 admits later release", "^0.2.0-rc.2", "0.2.1", true, ""},
		{"union covers both lines", "^0.1.0-rc.6 || ^0.2.0-rc.1", "0.2.0-rc.2", true, ""},
		{"union still stops at 0.3", "^0.1.0-rc.6 || ^0.2.0-rc.1", "0.3.0", false, KindOutOfRange},
		{"union reports the interesting clause", "^0.1.0-rc.6 || ^1.0.0", "0.2.0-rc.9", false, KindPreReleaseGate},
		{"tilde 0.1 stops at 0.2", "~0.1.0-rc.6", "0.2.0-rc.2", false, KindPreReleaseGate},
		// The ugly one: a lower bound that passes numerically and is still
		// refused, because npm needs a comparator naming 0.2.0 with a tag.
		{"open lower bound still gated", ">=0.1.0-rc.6", "0.2.0-rc.2", false, KindPreReleaseGate},
		{"release lower bound fails numerically", ">=0.2.0", "0.2.0-rc.1", false, KindOutOfRange},
		// Both rows are verbatim from published plugins, and together they are
		// the whole argument for the prescription profile mode prints: the
		// popular workaround (`|| >=0.2.0`) survives one rc line and then stops,
		// because the clause that was supposed to open the gate has no
		// prerelease tag on the core that needs it.
		{"ecosystem: dsh-m's three-clause union still gated on 0.2.2 rc",
			"^0.1.7-rc.2 || ^0.2.0-rc.1 || >=0.2.0", "0.2.2-rc.1", false, KindPreReleaseGate},
		{"ecosystem: naming the new core is what actually opens the gate",
			"^0.2.0-rc.1 || >=0.2.2-rc.1", "0.2.2-rc.1", true, ""},

		{"caret 1 spans minor", "^1.2.3", "1.9.9", true, ""},
		{"caret 1 stops at 2", "^1.2.3", "2.0.0", false, KindOutOfRange},
		{"caret does not admit lower rc", "^1.2.3", "1.2.3-rc.1", false, KindOutOfRange},
		{"exact pin", "1.2.3", "1.2.3", true, ""},
		{"exact pin misses patch", "1.2.3", "1.2.4", false, KindOutOfRange},
		{"partial means minor floor", "1.2", "1.2.9", true, ""},
		{"partial stops at next minor", "1.2", "1.3.0", false, KindOutOfRange},
		{"hyphen inclusive right", "1.2.3 - 2.3.4", "2.3.4", true, ""},
		{"hyphen exclusive past right", "1.2.3 - 2.3.4", "2.3.5", false, KindOutOfRange},
		{"hyphen partial right", "1.2.3 - 2.3", "2.3.9", true, ""},
		{"hyphen partial right stops", "1.2.3 - 2.3", "2.4.0", false, KindOutOfRange},
		{"operator with space", ">= 1.2.3", "1.2.3", true, ""},
		{"wildcard admits release", "*", "1.0.0", true, ""},
		{"wildcard refuses prerelease", "*", "1.0.0-rc.1", false, KindPreReleaseGate},
		{"caret 0.0.3 pins patch", "^0.0.3", "0.0.3", true, ""},
		{"caret 0.0.3 stops at next patch", "^0.0.3", "0.0.4", false, KindOutOfRange},
		{"tilde spans patch", "~1.2.3", "1.2.9", true, ""},
		{"tilde stops at next minor", "~1.2.3", "1.3.0", false, KindOutOfRange},
		{"caret x minor", "^1.2.x", "1.5.0", true, ""},
		{"caret 0.x spans zero line", "^0.x", "0.9.0", true, ""},
		{"caret 0.x stops at one", "^0.x", "1.0.0", false, KindOutOfRange},
		{"empty range is any", "", "0.1.0", true, ""},
		{"plain union member", "0.1.0-rc.6 || 0.2.0-rc.1", "0.1.0-rc.6", true, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rng, err := ParseRange(tc.rng)
			if err != nil {
				t.Fatalf("ParseRange(%q): %v", tc.rng, err)
			}
			v, err := Parse(tc.ver)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.ver, err)
			}
			exp := rng.Explain(v)
			if exp.Satisfied != tc.want {
				t.Fatalf("%q vs %s: satisfied=%v, want %v (detail: %s)",
					tc.rng, tc.ver, exp.Satisfied, tc.want, exp.Detail)
			}
			if tc.want {
				return
			}
			if exp.Kind != tc.kind {
				t.Errorf("kind = %q, want %q (detail: %s)", exp.Kind, tc.kind, exp.Detail)
			}
			if exp.Detail == "" || exp.Clause == "" {
				t.Errorf("rejection must explain itself, got %+v", exp)
			}
		})
	}
}

// TestGateIsNotOutOfRange pins the distinction the CLI prints. If the two kinds
// ever collapse into one, the report starts telling users to widen a range that
// is already wide enough, and the actual fix (opting into 0.2 prereleases) is
// never suggested.
func TestGateIsNotOutOfRange(t *testing.T) {
	rng, err := ParseRange("^0.1.0-rc.6")
	if err != nil {
		t.Fatal(err)
	}

	gated, _ := Parse("0.2.0-rc.2")
	exp := rng.Explain(gated)
	if exp.Kind != KindPreReleaseGate {
		t.Fatalf("0.2.0-rc.2: kind = %q, want %q", exp.Kind, KindPreReleaseGate)
	}
	if !strings.Contains(exp.Detail, "0.2.0") || !strings.Contains(exp.Detail, "prerelease") {
		t.Errorf("detail should name the core and the gate, got %q", exp.Detail)
	}

	out, _ := Parse("0.0.9")
	if got := rng.Explain(out).Kind; got != KindOutOfRange {
		t.Errorf("0.0.9: kind = %q, want %q", got, KindOutOfRange)
	}
}

func TestParseRangeRejectsGarbage(t *testing.T) {
	for _, s := range []string{"abc", "1.2.3 - ", "~1.2-beta", "1.2.3.4", ">>1"} {
		if r, err := ParseRange(s); err == nil {
			t.Errorf("ParseRange(%q) = %+v, want error", s, r)
		}
	}
}
