package codes

import (
	"regexp"
	"testing"
)

// The registry is data, and data rots silently. These assertions are about the
// shape of it rather than about any one code: a duplicate row, a typo'd
// severity, or an empty gloss makes the table a worse source of truth than no
// table, because internal/docs would then be checking the README against a
// broken definition.
func TestTableIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range All() {
		if c.ID == "" {
			t.Error("a code has an empty identifier")
			continue
		}
		if seen[c.ID] {
			t.Errorf("%q appears twice; Lookup would answer with the first", c.ID)
		}
		seen[c.ID] = true
		if c.Severity != Block && c.Severity != Warn {
			t.Errorf("%q: severity %q is neither block nor warn", c.ID, c.Severity)
		}
		if c.Scope != Any && c.Scope != Tarball {
			t.Errorf("%q: scope %q is neither any nor tarball", c.ID, c.Scope)
		}
		if c.Meaning == "" {
			t.Errorf("%q: no meaning; the table would be a list of bare strings", c.ID)
		}
	}
	if len(All()) != len(seen) {
		t.Fatalf("table has %d rows but %d distinct identifiers", len(All()), len(seen))
	}
}

// Identifiers are part of the JSON contract, so their shape is fixed: lowercase
// words joined by hyphens, no spaces, no underscores. A consumer that switches
// on them should never have to guess which spelling a version used.
func TestIdentifiersFollowTheWireShape(t *testing.T) {
	shape := regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)+$`)
	for _, c := range All() {
		if !shape.MatchString(c.ID) {
			t.Errorf("%q is not lowercase-hyphenated", c.ID)
		}
	}
}

func TestSeverityOf(t *testing.T) {
	if s, ok := SeverityOf(PeerRangeExcludesHost); !ok || s != Block {
		t.Errorf("peer-range-excludes-host = (%q,%v), want (block,true)", s, ok)
	}
	if s, ok := SeverityOf(EnginesDSHExcludesHost); !ok || s != Warn {
		t.Errorf("engines-dsh-excludes-host = (%q,%v), want (warn,true)", s, ok)
	}
	// An unknown code has no severity. Returning a default would make a typo in
	// a futures version look like a deliberate warn.
	if s, ok := SeverityOf("no-such-code"); ok || s != "" {
		t.Errorf("unknown code = (%q,%v), want (\"\",false)", s, ok)
	}
}

// All returns a copy: a caller that sorts the slice it got must not reorder the
// definition of what this tool reports.
func TestAllReturnsACopy(t *testing.T) {
	first := All()
	if len(first) == 0 {
		t.Fatal("registry is empty")
	}
	first[0].ID = "tampered"
	first[0].Severity = Warn
	if again := All(); again[0].ID == "tampered" || again[0].Severity != Block {
		t.Error("All() hands out its backing array")
	}
}

// The README table has a `(tarball only)` marker on exactly the archive codes,
// and the split has to be non-trivial: a registry where every code is "any"
// would make the marker untestable and the reverse assertion vacuous.
func TestScopesSplit(t *testing.T) {
	var any, tar int
	for _, c := range All() {
		switch c.Scope {
		case Any:
			any++
		case Tarball:
			tar++
		}
	}
	if any == 0 || tar == 0 {
		t.Fatalf("scopes: %d any, %d tarball — both must be non-empty", any, tar)
	}
}
