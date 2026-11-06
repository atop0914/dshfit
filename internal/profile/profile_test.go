package profile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pluginDir writes a real plugin tree. Fixtures are directories on disk rather
// than structs so a change in path handling fails here instead of passing a mock.
func pluginDir(t *testing.T, root, name, peers string) string {
	t.Helper()
	dir := filepath.Join(root, "plugins", name)
	patch := fmt.Sprintf("- insert:\n    - id: %s\n      name: %s\n", name, name)
	pkg := fmt.Sprintf(`{
      "name": %q, "version": "1.0.0",
      "files": ["lib", "cordis.patch.yml"],
      "peerDependencies": {%s},
      "dsh": {"bundle": {"patch": "./cordis.patch.yml"}}
    }`, name, peers)
	writeFile(t, filepath.Join(dir, "package.json"), pkg)
	writeFile(t, filepath.Join(dir, "cordis.patch.yml"), patch)
	return dir
}

func TestParseRealTranscript(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "profile", "pnpm-list-10.34.9.json"))
	if err != nil {
		t.Fatal(err)
	}
	projects, err := Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse the real pnpm transcript: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(projects))
	}
	if projects[0].Name != "dsh-profile" {
		t.Errorf("name = %q", projects[0].Name)
	}
	deps := projects[0].Deps()
	if len(deps) != 5 {
		t.Fatalf("got %d dependencies, want 5", len(deps))
	}
	for name, want := range map[string]string{
		"dsh-context": "0.82.1", "dsh-m": "0.12.7", "dsh-pet": "0.4.5",
		"dsh-plugin-catalog": "2026.1106.16122", "dsh-whale-widget": "0.4.2",
	} {
		got, ok := deps[name]
		if !ok {
			t.Errorf("%s missing from the transcript", name)
			continue
		}
		if got.Version != want {
			t.Errorf("%s version = %q, want %q", name, got.Version, want)
		}
		if got.Path == "" {
			t.Errorf("%s has no path in the transcript", name)
		}
	}
	// The transcript's paths belong to one machine and must not be required to
	// exist: parsing is the only claim this fixture makes.
	if _, err := os.Stat(deps["dsh-pet"].Path); err == nil {
		t.Log("transcript path happens to exist on this machine; the test still asserts nothing about it")
	}
}

// TestParseRejectsShapeThatWouldReadAsClean: `{}` and plain text both decode to
// "no projects" under a tolerant decoder, and an empty result is exactly what a
// passing run looks like. Both must be errors.
func TestParseRejectsShapeThatWouldReadAsClean(t *testing.T) {
	cases := map[string]string{
		"object":       `{"name": "web", "dependencies": {}}`,
		"plain text":   "dsh-pet 0.4.5\ndsh-context 0.82.1\n",
		"empty":        "   \n",
		"two values":   `[] []`,
		"truncated":    `[{"name": "web"`,
		"json null":    `null`,
		"leading word": `profile: []`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(body)); err == nil {
				t.Fatalf("Parse(%q) = nil error, want a refusal", body)
			}
		})
	}
}

func TestParseAcceptsEmptyArrayAndMissingSections(t *testing.T) {
	if projects, err := Parse(strings.NewReader(`[]`)); err != nil || len(projects) != 0 {
		t.Fatalf("Parse([]) = %v, %v", projects, err)
	}
	projects, err := Parse(strings.NewReader(`[{"name": "web", "path": "."}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || len(projects[0].Deps()) != 0 {
		t.Fatalf("got %+v", projects)
	}
}

// TestDepsPrefersDependencies: pnpm prints a package in each section it is
// declared in, and the same install must not be counted twice.
func TestDepsPrefersDependencies(t *testing.T) {
	p := Project{
		Dependencies:    map[string]Package{"x": {Version: "2.0.0"}},
		DevDependencies: map[string]Package{"x": {Version: "1.0.0"}, "y": {Version: "3.0.0"}},
	}
	deps := p.Deps()
	if len(deps) != 2 {
		t.Fatalf("got %d deps, want 2", len(deps))
	}
	if deps["x"].Version != "2.0.0" {
		t.Errorf("x = %q, want the dependencies entry", deps["x"].Version)
	}
	if deps["y"].Version != "3.0.0" {
		t.Errorf("y = %q, want the devDependencies entry", deps["y"].Version)
	}
}

// TestAssessGroupsOneRootCause covers the shape that made grouping worth
// building: twenty plugins making the same caret-0.x mistake are one row, not
// twenty.
func TestAssessGroupsOneRootCause(t *testing.T) {
	root := t.TempDir()
	deps := map[string]Package{}
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("dsh-plugin-%02d", i)
		deps[name] = Package{Version: "1.0.0", Path: pluginDir(t, root, name, `"@deepseek-ai/dsh-llm": "^0.2.0-rc.1"`)}
	}
	res, err := Assess([]Project{{Name: "web", Path: root, Dependencies: deps}}, "0.2.2-rc.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Counts.Blocked != 20 {
		t.Fatalf("blocked = %d, want 20", res.Counts.Blocked)
	}
	if len(res.Groups) != 1 {
		t.Fatalf("got %d groups, want 1: %+v", len(res.Groups), res.Groups)
	}
	g := res.Groups[0]
	if len(g.Plugins) != 20 {
		t.Errorf("group lists %d plugins, want 20", len(g.Plugins))
	}
	if len(g.Targets) != 1 || g.Targets[0] != "@deepseek-ai/dsh-llm" {
		t.Errorf("targets = %v", g.Targets)
	}
	if g.Reason != "prerelease-gate" {
		t.Errorf("reason = %q, want prerelease-gate", g.Reason)
	}
	if !strings.Contains(g.Fix, "prerelease gate") || !strings.Contains(g.Fix, ">=0.2.2-rc.1") {
		t.Errorf("fix should name the gate and the host: %q", g.Fix)
	}
}

// TestAssessKeepsDifferentReasonsApart is the reverse assertion that makes
// grouping safe: same code, same host, two diagnoses that need opposite fixes.
func TestAssessKeepsDifferentReasonsApart(t *testing.T) {
	root := t.TempDir()
	deps := map[string]Package{
		"dsh-narrow": {Path: pluginDir(t, root, "dsh-narrow", `"@deepseek-ai/dsh-llm": "^0.1.0-rc.6"`)},
		"dsh-gated":  {Path: pluginDir(t, root, "dsh-gated", `"@deepseek-ai/dsh-llm": "^0.2.0-rc.1"`)},
	}
	res, err := Assess([]Project{{Name: "web", Path: root, Dependencies: deps}}, "0.2.2-rc.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 2 {
		t.Fatalf("got %d groups, want 2 (out-of-range and prerelease-gate must not merge): %+v",
			len(res.Groups), res.Groups)
	}
	reasons := map[string]string{}
	for _, g := range res.Groups {
		reasons[g.Plugins[0]] = g.Reason
	}
	if reasons["dsh-narrow"] != "out-of-range" {
		t.Errorf("dsh-narrow reason = %q, want out-of-range", reasons["dsh-narrow"])
	}
	if reasons["dsh-gated"] != "prerelease-gate" {
		t.Errorf("dsh-gated reason = %q, want prerelease-gate", reasons["dsh-gated"])
	}
	for _, g := range res.Groups {
		if g.Plugins[0] == "dsh-narrow" && strings.Contains(g.Fix, "prerelease gate") {
			t.Errorf("the out-of-range group got the prerelease prescription: %q", g.Fix)
		}
	}
}

// TestAssessKeepsDifferentRangesApart: two ranges that differ as text are kept
// apart even when they reject for the same reason. Normalising them would mean
// deciding that two different edits are the same edit, and a wrong merge here
// hands every plugin in the group one author's fix.
func TestAssessKeepsDifferentRangesApart(t *testing.T) {
	root := t.TempDir()
	deps := map[string]Package{
		"dsh-a": {Path: pluginDir(t, root, "dsh-a", `"@deepseek-ai/dsh-llm": "^0.2.0-rc.1"`)},
		"dsh-b": {Path: pluginDir(t, root, "dsh-b", `"@deepseek-ai/dsh-llm": ">=0.2.0-rc.1 <0.3.0"`)},
	}
	res, err := Assess([]Project{{Name: "web", Path: root, Dependencies: deps}}, "0.2.2-rc.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 2 {
		t.Fatalf("got %d groups, want 2: %+v", len(res.Groups), res.Groups)
	}
}

// TestAssessNotAPluginIsNotBlocked is the case a real profile produced:
// dsh-plugin-catalog ships data and no bundle, and calling that a blocked plugin
// would teach every user to ignore the blocked count.
func TestAssessNotAPluginIsNotBlocked(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "plugins", "dsh-plugin-catalog")
	writeFile(t, filepath.Join(dir, "package.json"),
		`{"name":"dsh-plugin-catalog","version":"2026.1106.16122","files":["plugins.json"]}`)
	res, err := Assess([]Project{{Name: "web", Path: root, Dependencies: map[string]Package{
		"dsh-plugin-catalog": {Version: "2026.1106.16122", Path: dir},
	}}}, "0.2.2-rc.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Counts.Blocked != 0 {
		t.Fatalf("blocked = %d, want 0", res.Counts.Blocked)
	}
	if res.Counts.NotAPlugin != 1 {
		t.Fatalf("not-a-plugin = %d, want 1", res.Counts.NotAPlugin)
	}
	if len(res.Groups) != 0 {
		t.Errorf("a data package must not become a root cause: %+v", res.Groups)
	}
	e := res.Entries[0]
	if e.Status != StatusNotAPlugin {
		t.Errorf("status = %q", e.Status)
	}
	if !strings.Contains(e.Reason, "dsh.bundle.patch") {
		t.Errorf("reason should say what is missing: %q", e.Reason)
	}
}

// TestAssessMissingDirIsUnknownNotOK: "could not read it" and "it is fine" are
// the two states this tool exists to keep apart.
func TestAssessMissingDirIsUnknownNotOK(t *testing.T) {
	root := t.TempDir()
	res, err := Assess([]Project{{Name: "web", Path: root, Dependencies: map[string]Package{
		"dsh-gone": {Version: "0.1.0", Path: filepath.Join(root, "plugins", "dsh-gone")},
	}}}, "0.2.2-rc.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Counts.OK != 0 || res.Counts.Unknown != 1 {
		t.Fatalf("counts = %+v, want 0 ok and 1 unknown", res.Counts)
	}
	if res.Entries[0].Status != StatusUnknown {
		t.Errorf("status = %q", res.Entries[0].Status)
	}
	if !strings.Contains(res.Entries[0].Reason, "manifest unreadable") {
		t.Errorf("reason = %q", res.Entries[0].Reason)
	}
}

func TestAssessNoPathIsUnknown(t *testing.T) {
	res, err := Assess([]Project{{Name: "web", Dependencies: map[string]Package{
		"dsh-linked": {Version: "0.0.0", From: "link:../dsh-linked"},
	}}}, "0.2.2-rc.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Entries[0].Status != StatusUnknown {
		t.Fatalf("status = %q, want unknown", res.Entries[0].Status)
	}
	if !strings.Contains(res.Entries[0].Reason, "no directory") {
		t.Errorf("reason = %q", res.Entries[0].Reason)
	}
}

// TestAssessUnreadableManifestIsNotGrouped: an unreadable manifest has no range,
// so filing it under a compatibility root cause would invent an attribution.
func TestAssessUnreadableManifestIsNotGrouped(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "plugins", "dsh-broken")
	writeFile(t, filepath.Join(dir, "package.json"), `{"name": `)
	res, err := Assess([]Project{{Name: "web", Path: root, Dependencies: map[string]Package{
		"dsh-broken": {Path: dir},
	}}}, "0.2.2-rc.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Entries[0].Status != StatusUnknown {
		t.Fatalf("status = %q, want unknown", res.Entries[0].Status)
	}
	if len(res.Groups) != 0 {
		t.Errorf("groups = %+v, want none", res.Groups)
	}
}

func TestAssessEmptyListing(t *testing.T) {
	res, err := Assess(nil, "0.2.2-rc.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Counts != (Counts{}) {
		t.Errorf("counts = %+v, want all zero", res.Counts)
	}
	if len(res.Entries) != 0 || len(res.Groups) != 0 {
		t.Errorf("entries = %v, groups = %v", res.Entries, res.Groups)
	}
	if res.Host != "0.2.2-rc.1" {
		t.Errorf("host = %q", res.Host)
	}
}

// TestAssessIsByteStable: report order must not depend on map iteration, or the
// same profile inspected twice looks like it changed.
func TestAssessIsByteStable(t *testing.T) {
	root := t.TempDir()
	deps := map[string]Package{}
	for i, peer := range []string{`^0.1.0-rc.6`, `^0.2.0-rc.1`, `^0.2.0-rc.2`} {
		name := fmt.Sprintf("dsh-p%d", i)
		deps[name] = Package{Path: pluginDir(t, root, name, `"@deepseek-ai/dsh-llm": "`+peer+`"`)}
	}
	res, err := Assess([]Project{{Name: "web", Path: root, Dependencies: deps}}, "0.2.2-rc.1", "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := Assess([]Project{{Name: "web", Path: root, Dependencies: deps}}, "0.2.2-rc.1", "")
		if err != nil {
			t.Fatal(err)
		}
		second, err := json.Marshal(again)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("run %d differs:\n%s\n%s", i, first, second)
		}
	}
}

// TestAssessDuplicateNameIsAmbiguous: picking one of two installs silently would
// report a verdict about a directory the host may not load.
func TestAssessDuplicateNameIsAmbiguous(t *testing.T) {
	root := t.TempDir()
	a := pluginDir(t, filepath.Join(root, "a"), "dsh-dup", `"@deepseek-ai/dsh-llm": "^0.2.2-rc.1"`)
	b := pluginDir(t, filepath.Join(root, "b"), "dsh-dup", `"@deepseek-ai/dsh-llm": "^0.1.0-rc.6"`)
	res, err := Assess([]Project{
		{Name: "a", Path: root, Dependencies: map[string]Package{"dsh-dup": {Path: a}}},
		{Name: "b", Path: root, Dependencies: map[string]Package{"dsh-dup": {Path: b}}},
	}, "0.2.2-rc.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(res.Entries))
	}
	if res.Entries[0].Status != StatusUnknown {
		t.Fatalf("status = %q, want unknown", res.Entries[0].Status)
	}
	if !strings.Contains(res.Entries[0].Reason, "two directories") {
		t.Errorf("reason = %q", res.Entries[0].Reason)
	}
}

func TestAssessDedupesTheSameInstallAcrossSections(t *testing.T) {
	root := t.TempDir()
	dir := pluginDir(t, root, "dsh-once", `"@deepseek-ai/dsh-llm": "^0.2.2-rc.1"`)
	res, err := Assess([]Project{{
		Name:            "web",
		Path:            root,
		Dependencies:    map[string]Package{"dsh-once": {Path: dir}},
		DevDependencies: map[string]Package{"dsh-once": {Path: dir}},
	}}, "0.2.2-rc.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Counts.Packages != 1 {
		t.Fatalf("entries = %d, packages = %d, want 1 each", len(res.Entries), res.Counts.Packages)
	}
	if res.Counts.OK != 1 {
		t.Errorf("counts = %+v, want one ok", res.Counts)
	}
}

func TestAssessVersionFallsBackToManifest(t *testing.T) {
	root := t.TempDir()
	dir := pluginDir(t, root, "dsh-noversion", `"@deepseek-ai/dsh-llm": "^0.2.2-rc.1"`)
	res, err := Assess([]Project{{Name: "web", Path: root, Dependencies: map[string]Package{
		"dsh-noversion": {Path: dir},
	}}}, "0.2.2-rc.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Entries[0].Version != "1.0.0" {
		t.Errorf("version = %q, want the manifest's 1.0.0", res.Entries[0].Version)
	}
}

// TestAssessRelativePathResolvesAgainstProject keeps a hand-written listing
// usable, which is also what makes the fixtures portable: "./plugins/x" must mean
// "next to the listing", not "next to the shell's working directory".
func TestAssessRelativePathResolvesAgainstProject(t *testing.T) {
	root := t.TempDir()
	want := pluginDir(t, root, "dsh-rel", `"@deepseek-ai/dsh-llm": "^0.2.2-rc.1"`)
	res, err := Assess([]Project{{Name: "web", Path: ".", Dependencies: map[string]Package{
		"dsh-rel": {Path: "./plugins/dsh-rel"},
	}}}, "0.2.2-rc.1", root)
	if err != nil {
		t.Fatal(err)
	}
	if res.Counts.OK != 1 {
		t.Fatalf("counts = %+v, want one ok; entries=%+v", res.Counts, res.Entries)
	}
	if res.Entries[0].Dir != filepath.Clean(want) {
		t.Errorf("dir = %q, want %q", res.Entries[0].Dir, filepath.Clean(want))
	}
}

// TestAssessEnginesWarnStaysWarn: engines.dsh is never a block, and a profile
// report must not upgrade it while grouping.
func TestAssessEnginesWarnStaysWarn(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "plugins", "dsh-engines")
	writeFile(t, filepath.Join(dir, "package.json"), `{
      "name": "dsh-engines", "version": "1.0.0", "files": ["lib", "cordis.patch.yml"],
      "peerDependencies": {"@deepseek-ai/dsh-llm": "^0.2.2-rc.1"},
      "engines": {"dsh": "^0.1.0-rc.6"},
      "dsh": {"bundle": {"patch": "./cordis.patch.yml"}}}`)
	writeFile(t, filepath.Join(dir, "cordis.patch.yml"), "- insert:\n    - id: dsh-engines\n      name: dsh-engines\n")
	res, err := Assess([]Project{{Name: "web", Path: root, Dependencies: map[string]Package{
		"dsh-engines": {Path: dir},
	}}}, "0.2.2-rc.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Counts.Blocked != 0 || res.Counts.Warn != 1 {
		t.Fatalf("counts = %+v, want one warn and nothing blocked", res.Counts)
	}
	if len(res.Groups) != 1 || res.Groups[0].Severity != "warn" {
		t.Fatalf("groups = %+v, want one warn group", res.Groups)
	}
}

// TestPrescriptionsAreDistinctPerCode makes sure no code silently falls through
// to the "no prescription" default: a group with no fix is a dead end.
func TestPrescriptionsAreDistinctPerCode(t *testing.T) {
	host := "0.2.2-rc.1"
	for _, code := range []string{
		"peer-range-excludes-host", "unparseable-peer-range", "engines-dsh-excludes-host",
		"patch-missing-from-files", "patch-unreadable", "patch-name-mismatch",
		"patch-row-without-id", "unparseable-engines-dsh",
	} {
		fix := prescription(Group{Code: code, Range: "x"}, host)
		if fix == "" || strings.Contains(fix, "no automatic prescription") {
			t.Errorf("%s has no prescription: %q", code, fix)
		}
	}
}
