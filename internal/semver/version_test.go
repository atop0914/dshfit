package semver

import "testing"

func TestParseAndCompare(t *testing.T) {
	order := []string{
		"0.1.0-rc.6",
		"0.1.0",
		"0.2.0-rc.1",
		"0.2.0-rc.2",
		"0.2.0-rc.10", // numeric identifier: 10 after 2, not before
		"0.2.0",
		"0.2.1",
	}
	for i := 0; i < len(order); i++ {
		a, err := Parse(order[i])
		if err != nil {
			t.Fatalf("Parse(%q): %v", order[i], err)
		}
		for j := 0; j < len(order); j++ {
			b, err := Parse(order[j])
			if err != nil {
				t.Fatalf("Parse(%q): %v", order[j], err)
			}
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got := Compare(a, b); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", a, b, got, want)
			}
		}
	}
}

func TestNumericIdentifiersRankBelowAlphanumeric(t *testing.T) {
	// The spec makes "1.0.0-2" precede "1.0.0-rc". Getting this backwards would
	// reorder rc candidate chains, which is how a solver picks the wrong pin.
	a, _ := Parse("1.0.0-2")
	b, _ := Parse("1.0.0-rc")
	if Compare(a, b) != -1 {
		t.Fatalf("Compare(1.0.0-2, 1.0.0-rc) = %d, want -1", Compare(a, b))
	}
}

func TestParseRejectsPartial(t *testing.T) {
	for _, s := range []string{"0.2", "1", "*", "", "1.2.3.4", "v", "1.x"} {
		if v, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) = %v, want error", s, v)
		}
	}
}

func TestParseAcceptsVPrefixAndBuild(t *testing.T) {
	v, err := Parse("v0.2.0-rc.2+sha.abc")
	if err != nil {
		t.Fatal(err)
	}
	if v.Core() != "0.2.0" || !v.HasPreRelease() {
		t.Fatalf("got %+v", v)
	}
	if v.String() != "0.2.0-rc.2+sha.abc" {
		t.Fatalf("String() = %q", v.String())
	}
}
