package profile

import (
	"encoding/json"
	"testing"

	"github.com/atop0914/dshfit/internal/compat"
)

// --- fixtures -------------------------------------------------------------

func diffIssue(code, reason string) compat.Issue {
	return compat.Issue{Severity: compat.Block, Code: code, Reason: reason, Detail: "detail for " + code}
}

func judgedEntry(name, version string, verdict compat.Verdict, issues ...compat.Issue) Entry {
	return Entry{
		Name:    name,
		Version: version,
		Status:  StatusPlugin,
		Report:  &compat.Report{Plugin: name, Verdict: verdict, Issues: issues},
	}
}

func okEntry(name string) Entry { return judgedEntry(name, "1.0.0", compat.OK) }

func blockedEntry(name string, issues ...compat.Issue) Entry {
	return judgedEntry(name, "1.0.0", compat.Blocked, issues...)
}

func notAPluginEntry(name string) Entry {
	return Entry{Name: name, Version: "1.0.0", Status: StatusNotAPlugin, Reason: "no dsh.bundle.patch"}
}

func unknownEntry(name string) Entry {
	return Entry{Name: name, Version: "1.0.0", Status: StatusUnknown, Reason: "manifest unreadable: no such file"}
}

// diffResult builds a Result the way Assess would, with entries already sorted
// by name — Compare must not depend on that, but the fixture should look real.
func diffResult(host string, entries ...Entry) Result {
	return Result{Host: host, Entries: entries, Counts: countEntries(entries), Groups: []Group{}}
}

func onlyChange(t *testing.T, d Diff) Change {
	t.Helper()
	if len(d.Changes) != 1 {
		t.Fatalf("want exactly 1 change, got %d: %+v", len(d.Changes), d.Changes)
	}
	return d.Changes[0]
}

// --- the three movement kinds --------------------------------------------

// A plugin that loads today and is blocked after the upgrade is the line the
// user ran the command for.
func TestCompareReportsLoadabilityDrop(t *testing.T) {
	a := diffResult("0.2.1-rc.3", okEntry("dsh-pet"))
	b := diffResult("0.2.2-rc.1", blockedEntry("dsh-pet", diffIssue("peer-range-excludes-host", "prerelease-gate")))

	d := Compare(a, b)

	ch := onlyChange(t, d)
	if ch.Kind != KindWorse {
		t.Fatalf("kind = %q, want %q", ch.Kind, KindWorse)
	}
	if ch.Name != "dsh-pet" {
		t.Errorf("name = %q", ch.Name)
	}
	if ch.From.Verdict != compat.OK || ch.To.Verdict != compat.Blocked {
		t.Errorf("verdicts = %q -> %q, want ok -> blocked", ch.From.Verdict, ch.To.Verdict)
	}
	if len(ch.To.Causes) != 1 || ch.To.Causes[0] != "peer-range-excludes-host(prerelease-gate)" {
		t.Errorf("to.causes = %v", ch.To.Causes)
	}
	if d.Counts.Worse != 1 || d.Counts.Total() != 1 {
		t.Errorf("counts = %+v", d.Counts)
	}
}

func TestCompareReportsLoadabilityRestored(t *testing.T) {
	a := diffResult("0.2.1-rc.3", blockedEntry("dsh-cap", diffIssue("peer-range-excludes-host", "out-of-range")))
	b := diffResult("0.2.2-rc.1", okEntry("dsh-cap"))

	ch := onlyChange(t, Compare(a, b))

	if ch.Kind != KindBetter {
		t.Fatalf("kind = %q, want %q", ch.Kind, KindBetter)
	}
	if len(ch.To.Causes) != 0 {
		t.Errorf("a cleared plugin must carry no causes, got %v", ch.To.Causes)
	}
}

// The flagship case: identical verdict on both sides, opposite prescription.
// A diff that compares verdict strings only silently drops it.
func TestCompareSameBlockDifferentReasonIsAChange(t *testing.T) {
	a := diffResult("0.2.1-rc.3", blockedEntry("dsh-m", diffIssue("peer-range-excludes-host", "out-of-range")))
	b := diffResult("0.3.0", blockedEntry("dsh-m", diffIssue("peer-range-excludes-host", "prerelease-gate")))

	ch := onlyChange(t, Compare(a, b))

	if ch.Kind != KindCause {
		t.Fatalf("kind = %q, want %q (blocked->blocked with a different reason is the "+
			"one line the user must not miss)", ch.Kind, KindCause)
	}
	if ch.From.Verdict != compat.Blocked || ch.To.Verdict != compat.Blocked {
		t.Fatalf("verdicts = %q -> %q, want blocked -> blocked", ch.From.Verdict, ch.To.Verdict)
	}
	if got := ch.From.Causes[0]; got != "peer-range-excludes-host(out-of-range)" {
		t.Errorf("from.causes = %v", ch.From.Causes)
	}
	if got := ch.To.Causes[0]; got != "peer-range-excludes-host(prerelease-gate)" {
		t.Errorf("to.causes = %v", ch.To.Causes)
	}
}

// Reverse assertion for the same rule: a block that does not change must not
// be reported. If this ever goes red, the tool has started reporting the
// upgrade as the cause of a problem that predates it.
func TestCompareIdenticalOutcomeIsNotAChange(t *testing.T) {
	issues := []compat.Issue{
		diffIssue("peer-range-excludes-host", "prerelease-gate"),
		diffIssue("engines-dsh-excludes-host", ""),
	}
	a := diffResult("0.2.1-rc.3", blockedEntry("dsh-pet", issues...), okEntry("dsh-context"))
	b := diffResult("0.2.2-rc.1", blockedEntry("dsh-pet", issues...), okEntry("dsh-context"))

	d := Compare(a, b)

	if len(d.Changes) != 0 {
		t.Fatalf("want no changes, got %+v", d.Changes)
	}
	if d.Changes == nil {
		t.Error("changes must be an empty slice, not nil: nil marshals to null and reads as \"not computed\"")
	}
	if d.Counts.Total() != 0 {
		t.Errorf("counts = %+v", d.Counts)
	}
	if d.ToCounts.Blocked != 1 {
		t.Errorf("to_counts must still report the 1 block that exists on both hosts, got %+v", d.ToCounts)
	}
}

// --- presence -------------------------------------------------------------

func TestCompareReportsPackagesOnOneSideOnly(t *testing.T) {
	a := diffResult("0.2.1-rc.3", okEntry("dsh-kept"), okEntry("dsh-old"))
	b := diffResult("0.2.2-rc.1", okEntry("dsh-kept"), okEntry("dsh-new"))

	d := Compare(a, b)

	if len(d.Changes) != 2 {
		t.Fatalf("want 2 changes, got %d: %+v", len(d.Changes), d.Changes)
	}
	if d.Changes[0].Kind != KindAdded || d.Changes[0].Name != "dsh-new" {
		t.Errorf("first change = %+v, want added dsh-new", d.Changes[0])
	}
	if d.Changes[0].From.Present {
		t.Error("an added package must report from.present = false")
	}
	if d.Changes[1].Kind != KindRemoved || d.Changes[1].Name != "dsh-old" {
		t.Errorf("second change = %+v, want removed dsh-old", d.Changes[1])
	}
	if d.Counts.Added != 1 || d.Counts.Removed != 1 {
		t.Errorf("counts = %+v", d.Counts)
	}
}

// A package that appears in the target listing is reported with the state it
// arrives in, because "new" alone says nothing about whether it will load. A
// data package that is not a plugin is not a headline: it cannot break.
func TestCompareAddedPackageCarriesItsOwnVerdict(t *testing.T) {
	a := diffResult("0.2.1-rc.3", okEntry("dsh-kept"))
	b := diffResult("0.2.2-rc.1", okEntry("dsh-kept"),
		notAPluginEntry("dsh-catalog"),
		blockedEntry("dsh-new", diffIssue("peer-range-excludes-host", "out-of-range")))

	d := Compare(a, b)

	if len(d.Changes) != 2 {
		t.Fatalf("want 2 changes, got %+v", d.Changes)
	}
	catalog, fresh := d.Changes[0], d.Changes[1]
	if catalog.Kind != KindAdded || catalog.Name != "dsh-catalog" {
		t.Fatalf("first change = %+v", catalog)
	}
	if catalog.To.Status != StatusNotAPlugin {
		t.Errorf("added not-a-plugin: to.status = %q", catalog.To.Status)
	}
	if catalog.To.Verdict != "" {
		t.Errorf("a package that was not judged must carry no verdict, got %q", catalog.To.Verdict)
	}
	if fresh.Name != "dsh-new" || fresh.To.Verdict != compat.Blocked {
		t.Errorf("second change = %+v, want dsh-new arriving blocked", fresh)
	}
	// Presence changes are their own kind. Reporting an addition as "worse"
	// would claim the upgrade broke something that was never there.
	if d.Counts.Worse != 0 {
		t.Errorf("worse = %d, want 0: an addition is not a regression", d.Counts.Worse)
	}
}

// --- classification edges -------------------------------------------------

// A warning that appears is a real difference, but it is not a loadability
// drop: the plugin still loads. Printing it under "will stop loading" would be
// the cheapest way to lose the reader's trust.
func TestCompareNewWarningIsCauseNotWorse(t *testing.T) {
	a := diffResult("0.2.1-rc.3", okEntry("dsh-context"))
	b := diffResult("0.2.2-rc.1", judgedEntry("dsh-context", "1.0.0", compat.Warning,
		compat.Issue{Severity: compat.Warn, Code: "engines-dsh-excludes-host", Detail: "d"}))

	ch := onlyChange(t, Compare(a, b))

	if ch.Kind == KindWorse {
		t.Fatalf("a warning must not be reported as a loadability drop (kind = %q)", ch.Kind)
	}
	if ch.Kind != KindCause {
		t.Errorf("kind = %q, want %q", ch.Kind, KindCause)
	}
	if ch.From.Verdict != compat.OK || ch.To.Verdict != compat.Warning {
		t.Errorf("verdicts = %q -> %q", ch.From.Verdict, ch.To.Verdict)
	}
}

// "Could not be judged" is never equal to "fine". A package whose manifest
// stopped being readable must surface, with the reason attached.
func TestCompareJudgeabilityLossIsReported(t *testing.T) {
	a := diffResult("0.2.1-rc.3", okEntry("dsh-x"))
	b := diffResult("0.2.2-rc.1", unknownEntry("dsh-x"))

	ch := onlyChange(t, Compare(a, b))

	if ch.Kind != KindCause {
		t.Fatalf("kind = %q, want %q", ch.Kind, KindCause)
	}
	if ch.To.Status != StatusUnknown || ch.To.Reason == "" {
		t.Errorf("unknown side must carry status and reason, got %+v", ch.To)
	}
	if ch.To.Verdict != "" {
		t.Errorf("unknown side must not claim a verdict, got %q", ch.To.Verdict)
	}
}

// A version bump that changes nothing this tool has an opinion about is not a
// finding: the command answers "will it load", not "what got published".
func TestCompareVersionOnlyDifferenceIsNotAChange(t *testing.T) {
	a := diffResult("0.2.1-rc.3", okEntry("dsh-x"))
	b := diffResult("0.2.2-rc.1", judgedEntry("dsh-x", "2.5.0", compat.OK))

	if d := Compare(a, b); len(d.Changes) != 0 {
		t.Fatalf("a version bump with an unchanged verdict is not a difference, got %+v", d.Changes)
	}
}

func TestCompareSortsByKindThenName(t *testing.T) {
	a := diffResult("0.2.1-rc.3",
		okEntry("b-worse"),
		blockedEntry("c-better", diffIssue("peer-range-excludes-host", "out-of-range")),
		blockedEntry("e-cause", diffIssue("peer-range-excludes-host", "out-of-range")),
		okEntry("zz-removed"),
	)
	b := diffResult("0.2.2-rc.1",
		blockedEntry("b-worse", diffIssue("peer-range-excludes-host", "out-of-range")),
		okEntry("c-better"),
		blockedEntry("e-cause", diffIssue("peer-range-excludes-host", "prerelease-gate")),
		okEntry("a-added"),
	)

	d := Compare(a, b)

	wantOrder := []struct {
		kind ChangeKind
		name string
	}{
		{KindWorse, "b-worse"},
		{KindBetter, "c-better"},
		{KindCause, "e-cause"},
		{KindAdded, "a-added"},
		{KindRemoved, "zz-removed"},
	}
	if len(d.Changes) != len(wantOrder) {
		t.Fatalf("want %d changes, got %d: %+v", len(wantOrder), len(d.Changes), d.Changes)
	}
	for i, want := range wantOrder {
		if d.Changes[i].Kind != want.kind || d.Changes[i].Name != want.name {
			t.Errorf("changes[%d] = %q/%q, want %q/%q",
				i, d.Changes[i].Kind, d.Changes[i].Name, want.kind, want.name)
		}
	}
}

// Two runs over the same pair must produce byte-identical JSON. A diff that
// reorders itself between runs cannot be reviewed, committed, or compared.
func TestCompareIsReproducible(t *testing.T) {
	a := diffResult("0.2.1-rc.3",
		okEntry("dsh-a"), okEntry("dsh-b"),
		blockedEntry("dsh-c", diffIssue("peer-range-excludes-host", "out-of-range"),
			diffIssue("engines-dsh-excludes-host", "")),
	)
	b := diffResult("0.2.2-rc.1",
		blockedEntry("dsh-a", diffIssue("peer-range-excludes-host", "prerelease-gate")),
		okEntry("dsh-b"),
		blockedEntry("dsh-c", diffIssue("engines-dsh-excludes-host", ""),
			diffIssue("peer-range-excludes-host", "out-of-range")),
		okEntry("dsh-d"),
	)

	first, err := json.Marshal(Compare(a, b))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	second, err := json.Marshal(Compare(a, b))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("two runs differ:\n%s\n%s", first, second)
	}

	// Cause order must not depend on the order issues were reported in: dsh-c
	// carries its two issues in opposite order on the two sides and both sides
	// must render the same label list.
	var d Diff
	if err := json.Unmarshal(first, &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, ch := range d.Changes {
		if ch.Name != "dsh-c" {
			continue
		}
		if len(ch.From.Causes) != 2 || len(ch.To.Causes) != 2 {
			t.Fatalf("causes = %v / %v", ch.From.Causes, ch.To.Causes)
		}
		if ch.From.Causes[0] != ch.To.Causes[0] || ch.From.Causes[1] != ch.To.Causes[1] {
			t.Fatalf("cause labels are not sorted: %v vs %v", ch.From.Causes, ch.To.Causes)
		}
	}
}

// Same result on both sides is the "nothing to see" case, and it must be
// exactly one entry-free result — not a single row of noise per package.
func TestCompareIdenticalResults(t *testing.T) {
	r := diffResult("0.2.1-rc.3", okEntry("a"), blockedEntry("b", diffIssue("peer-range-excludes-host", "out-of-range")))

	d := Compare(r, r)

	if len(d.Changes) != 0 || d.Counts.Total() != 0 {
		t.Fatalf("want no changes, got %+v / %+v", d.Changes, d.Counts)
	}
	if d.From != r.Host || d.To != r.Host {
		t.Errorf("hosts = %q -> %q", d.From, d.To)
	}
}
