package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atop0914/dshfit/internal/profile"
)

// driftFixture is the listing built to differ between 0.2.1-rc.3 and
// 0.2.2-rc.1: one plugin pinned to the older line, one pinned to the newer
// line, and one refused by the prerelease gate on both — the last is what
// proves an unchanged block stays out of the report.
func driftFixture() string {
	return filepath.Join("..", "..", "testdata", "profile", "drift", "list.json")
}

func TestDiffRequiresBothHosts(t *testing.T) {
	for _, args := range [][]string{
		{"diff", "--to", "0.2.2-rc.1", driftFixture()},
		{"diff", "--from", "0.2.1-rc.3", driftFixture()},
		{"diff", driftFixture()},
	} {
		code, _, stderr := run(t, args...)
		if code != 2 {
			t.Fatalf("%v: code = %d, want 2", args, code)
		}
		if !strings.Contains(stderr, "--from and --to are both required") {
			t.Errorf("%v: stderr = %q", args, stderr)
		}
	}
}

func TestDiffRequiresExactlyOneListing(t *testing.T) {
	for _, args := range [][]string{
		{"diff", "--from", "0.2.1-rc.3", "--to", "0.2.2-rc.1"},
		{"diff", "--from", "0.2.1-rc.3", "--to", "0.2.2-rc.1", "a.json", "b.json"},
	} {
		code, _, stderr := run(t, args...)
		if code != 2 {
			t.Fatalf("%v: code = %d, want 2", args, code)
		}
		if !strings.Contains(stderr, "exactly one") {
			t.Errorf("%v: stderr = %q", args, stderr)
		}
	}
}

// TestDiffPrintsOnlyRealChanges is the deliverable: a plugin that drops, a
// plugin that heals, and — by absence — the plugin blocked on both hosts,
// which is not a change.
func TestDiffPrintsOnlyRealChanges(t *testing.T) {
	code, stdout, stderr := run(t, "diff", "--from", "0.2.1-rc.3", "--to", "0.2.2-rc.1", driftFixture())
	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr=%s", code, stderr)
	}
	for _, want := range []string{
		"host 0.2.1-rc.3 → 0.2.2-rc.1",
		"3 package(s), 2 differ",
		"will stop loading — 1",
		"dsh-pinned@1.0.0  ok → blocked",
		"now: peer-range-excludes-host(prerelease-gate)",
		"improves — 1",
		"dsh-ships@1.0.0  blocked → ok",
		"cleared: peer-range-excludes-host(out-of-range)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	// dsh-stable is blocked by the prerelease gate on both hosts. Printing it
	// would blame the upgrade for a problem that predates it — and would teach
	// the reader to skim the sections that matter.
	if strings.Contains(stdout, "dsh-stable") {
		t.Errorf("a block that exists on both hosts must not appear:\n%s", stdout)
	}
}

// TestDiffBlockedToBlockedReasonIsPrinted is the case a verdict-string
// comparison loses: same verdict, opposite prescription. The two lines must be
// printed together, or the reader cannot tell which edit to make.
func TestDiffBlockedToBlockedReasonIsPrinted(t *testing.T) {
	code, stdout, stderr := run(t, "diff", "--from", "0.2.2-rc.1", "--to", "0.3.0", profileFixture())
	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr=%s", code, stderr)
	}
	for _, want := range []string{
		"root cause changed — 1",
		"dsh-pet@0.4.5  blocked → blocked",
		"before: peer-range-excludes-host(prerelease-gate)",
		"after:  peer-range-excludes-host(out-of-range)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	// dsh-narrow-range is out of range on both hosts, so it is not a change.
	if strings.Contains(stdout, "dsh-narrow-range") {
		t.Errorf("an unchanged block must not appear:\n%s", stdout)
	}
}

// TestDiffWithoutDriftExitsZero: two hosts that behave identically for this
// profile produce one line, not an empty table, and exit 0.
func TestDiffWithoutDriftExitsZero(t *testing.T) {
	code, stdout, stderr := run(t, "diff", "--from", "0.2.1-rc.3", "--to", "0.2.2-rc.1", profileFixture())
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "no differences") {
		t.Fatalf("stdout should say so explicitly:\n%s", stdout)
	}
	// "No differences" is not a clean bill of health, and the text must not let
	// a reader take it as one: the same two plugins are blocked either way.
	if !strings.Contains(stdout, "not that the profile is healthy") {
		t.Errorf("stdout should qualify the zero-diff result:\n%s", stdout)
	}
	if strings.Contains(stdout, "will stop loading") {
		t.Errorf("no section headers when nothing differs:\n%s", stdout)
	}
}

func TestDiffJSONMatchesTextAndIsReproducible(t *testing.T) {
	code, first, stderr := run(t, "diff", "--from", "0.2.1-rc.3", "--to", "0.2.2-rc.1", "--json", driftFixture())
	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr=%s", code, stderr)
	}
	_, second, _ := run(t, "diff", "--from", "0.2.1-rc.3", "--to", "0.2.2-rc.1", "--json", driftFixture())
	if first != second {
		t.Fatalf("two runs differ:\n%s\n%s", first, second)
	}

	var d profile.Diff
	if err := json.Unmarshal([]byte(first), &d); err != nil {
		t.Fatalf("decode: %v\n%s", err, first)
	}
	if d.From != "0.2.1-rc.3" || d.To != "0.2.2-rc.1" {
		t.Errorf("hosts = %q -> %q", d.From, d.To)
	}
	if d.Counts.Worse != 1 || d.Counts.Better != 1 || d.Counts.Cause != 0 ||
		d.Counts.Added != 0 || d.Counts.Removed != 0 {
		t.Errorf("counts = %+v", d.Counts)
	}
	if d.Counts.Total() != len(d.Changes) {
		t.Errorf("counts total %d != %d changes", d.Counts.Total(), len(d.Changes))
	}
	// The target host's own summary travels with the diff, so a JSON consumer
	// learns that dsh-stable is blocked without running a second command.
	if d.ToCounts.Blocked != 2 || d.ToCounts.Packages != 3 {
		t.Errorf("to_counts = %+v", d.ToCounts)
	}
	if d.Changes[0].Kind != profile.KindWorse || d.Changes[0].Name != "dsh-pinned" {
		t.Errorf("changes[0] = %+v", d.Changes[0])
	}
}

func TestDiffRejectsUnparseableHost(t *testing.T) {
	code, _, stderr := run(t, "diff", "--from", "not-a-version", "--to", "0.2.2-rc.1", driftFixture())
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "host version") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestDiffReadsStdin(t *testing.T) {
	data, err := os.ReadFile(driftFixture())
	if err != nil {
		t.Fatal(err)
	}
	// Stdin has no listing directory, so the relative plugin paths do not
	// resolve and both hosts end up with three unjudged packages. That is a
	// zero change — and the note must say why, because three unjudged packages
	// reported as "no differences" alone would read as a clean bill.
	code, stdout, stderr := runWithStdin(t, string(data), "diff", "--from", "0.2.1-rc.3", "--to", "0.2.2-rc.1", "-")
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "3 package(s)") || !strings.Contains(stdout, "no differences") {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stdout, "3 unjudged package(s)") {
		t.Errorf("stdout should not hide the unjudged packages:\n%s", stdout)
	}
}
