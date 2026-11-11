package profile

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// renderMarkdown runs the renderer over a Result built from real directories,
// so a change in path handling or grouping shows up here too.
func renderMarkdown(t *testing.T, res Result) string {
	t.Helper()
	var b strings.Builder
	if err := Markdown(&b, res); err != nil {
		t.Fatalf("render markdown: %v", err)
	}
	return b.String()
}

// plainDir writes a package that is deliberately not a plugin: no
// dsh.bundle.patch. In a profile that is ordinary, and it must stay out of every
// count that talks about plugins.
func plainDir(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, "plugins", name)
	writeFile(t, filepath.Join(dir, "package.json"), `{"name": "`+name+`", "version": "2.0.0"}`)
	return dir
}

func mustAssess(t *testing.T, deps map[string]string, host string) Result {
	t.Helper()
	root := t.TempDir()
	pkgs := make(map[string]Package, len(deps))
	for name, path := range deps {
		pkgs[name] = Package{From: name, Path: path}
	}
	res, err := Assess([]Project{{Name: "profile", Path: root, Dependencies: pkgs}}, host, "")
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	return res
}

// TestMarkdownBlockedNamesEveryPluginAndTheSmallestFix is the CI contract: a
// reader who stops after the table still knows which plugins drop and what to
// write instead.
func TestMarkdownBlockedNamesEveryPluginAndTheSmallestFix(t *testing.T) {
	root := t.TempDir()
	a := pluginDir(t, root, "dsh-a", `"@deepseek-ai/dsh-app-boot": "^0.1.0-rc.6"`)
	b := pluginDir(t, root, "dsh-b", `"@deepseek-ai/dsh-app-boot": "^0.1.0-rc.6"`)
	c := plainDir(t, root, "dsh-catalog")
	res := mustAssess(t, map[string]string{"dsh-a": a, "dsh-b": b, "dsh-catalog": c}, "0.3.0")

	if res.Counts.Packages != 3 || res.Counts.Plugins != 2 || res.Counts.Blocked != 2 {
		t.Fatalf("counts = %+v, want 3 packages, 2 plugins, 2 blocked", res.Counts)
	}
	md := renderMarkdown(t, res)

	// The plugin count in the headline is what a mutation from Counts to
	// len(Entries) breaks first: 3 packages are not 3 plugins.
	for _, want := range []string{
		"2 of 2 plugin(s) will not load on `0.3.0`",
		"`peer-range-excludes-host` (`out-of-range`)",
		"`^0.1.0-rc.6 \\|\\| >=0.3.0`",
		"`dsh-a`",
		"`dsh-b`",
		"What the smallest fix promises:",
		"3 package(s): 0 ok, 0 warn, 2 blocked, 0 unknown, 1 not a plugin",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown is missing %q\n---\n%s", want, md)
		}
	}
	// The two rejection kinds must stay distinguishable here too: the CI summary
	// is where an author reads the prescription first.
	if strings.Contains(md, "incompatible") {
		t.Errorf("markdown merged the two rejections into one word:\n%s", md)
	}
}

// TestMarkdownCleanRunPredictsNoBreak is a reverse assertion: a run with
// nothing blocked and nothing unread must not contain a single sentence that
// predicts a break. Crying wolf on a clean profile is how a CI gate gets
// switched off within a week.
func TestMarkdownCleanRunPredictsNoBreak(t *testing.T) {
	root := t.TempDir()
	a := pluginDir(t, root, "dsh-fine", `"@deepseek-ai/dsh-app-boot": ">=0.2.2-rc.1"`)
	c := plainDir(t, root, "dsh-catalog")
	res := mustAssess(t, map[string]string{"dsh-fine": a, "dsh-catalog": c}, "0.3.0")

	if res.Counts.Blocked != 0 || res.Counts.Unknown != 0 || len(res.Groups) != 0 {
		t.Fatalf("fixture is not clean: %+v groups=%+v", res.Counts, res.Groups)
	}
	md := renderMarkdown(t, res)

	if !strings.Contains(md, "Nothing to fix on `0.3.0`: all 1 plugin(s) load.") {
		t.Errorf("clean run has no plain statement of the good news:\n%s", md)
	}
	for _, forbidden := range []string{
		"will not load", "could not be judged", "Incomplete", "⚠", "| cause |",
	} {
		if strings.Contains(md, forbidden) {
			t.Errorf("clean run contains break wording %q:\n%s", forbidden, md)
		}
	}
	if !strings.Contains(md, "No findings — 1 plugin(s)") {
		t.Errorf("clean run does not list what was verified:\n%s", md)
	}
}

// TestMarkdownUnknownIsNotAnAllClear is the other reverse assertion, and the one
// this whole tool is built around: "could not be read" must never render as
// "nothing to fix", and the run must still be marked incomplete.
func TestMarkdownUnknownIsNotAnAllClear(t *testing.T) {
	root := t.TempDir()
	ok := pluginDir(t, root, "dsh-fine", `"@deepseek-ai/dsh-app-boot": ">=0.2.2-rc.1"`)
	res := mustAssess(t, map[string]string{
		"dsh-fine": ok,
		"dsh-gone": filepath.Join(root, "plugins", "dsh-gone"),
	}, "0.3.0")

	if res.Counts.Unknown != 1 || res.Counts.Blocked != 0 {
		t.Fatalf("counts = %+v, want one unknown and nothing blocked", res.Counts)
	}
	md := renderMarkdown(t, res)

	for _, want := range []string{
		"Incomplete",
		"could not be judged",
		"These are not compatible — they are unread.",
		"`dsh-gone`",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown is missing %q\n---\n%s", want, md)
		}
	}
	for _, forbidden := range []string{
		"Nothing to fix",
		"all 1 plugin(s) load",
	} {
		if strings.Contains(md, forbidden) {
			t.Errorf("an unread package rendered as %q:\n%s", forbidden, md)
		}
	}
}

// TestMarkdownWarnOnlyIsNotNothingToFix: warnings are findings. They never block
// a load, so no break wording, but "nothing to fix" would hide the one place an
// author wrote down their support window.
func TestMarkdownWarnOnlyIsNotNothingToFix(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "plugins", "dsh-engines")
	writeFile(t, filepath.Join(dir, "package.json"), `{
      "name": "dsh-engines", "version": "1.0.0", "files": ["cordis.patch.yml"],
      "peerDependencies": {"@deepseek-ai/dsh-llm": "^0.2.2-rc.1"},
      "engines": {"dsh": "^0.1.0-rc.6"},
      "dsh": {"bundle": {"patch": "./cordis.patch.yml"}}}`)
	writeFile(t, filepath.Join(dir, "cordis.patch.yml"), "- insert:\n    - id: dsh-engines\n      name: dsh-engines\n")

	res := mustAssess(t, map[string]string{"dsh-engines": dir}, "0.2.2-rc.1")
	md := renderMarkdown(t, res)

	if !strings.Contains(md, "All 1 plugin(s) load on `0.2.2-rc.1`, with 1 warning(s).") {
		t.Errorf("warn-only headline is wrong:\n%s", md)
	}
	for _, forbidden := range []string{"Nothing to fix", "will not load", "Incomplete"} {
		if strings.Contains(md, forbidden) {
			t.Errorf("warn-only run contains %q:\n%s", forbidden, md)
		}
	}
	if !strings.Contains(md, "#### Warnings — 1 cause(s), 1 plugin(s)") {
		t.Errorf("warn group is not shown:\n%s", md)
	}
}

// TestMarkdownKeepsThirdPartyTextInsideItsCell: package names come from someone
// else's package.json, and this output is rendered as markdown in a CI summary.
// A bare pipe ends the cell and a lost cell shifts every column after it.
func TestMarkdownKeepsThirdPartyTextInsideItsCell(t *testing.T) {
	root := t.TempDir()
	piped := plainDir(t, root, "evil")
	res := mustAssess(t, map[string]string{"evil|name": piped}, "0.3.0")
	md := renderMarkdown(t, res)

	if !strings.Contains(md, "evil\\|name@2.0.0") {
		t.Errorf("a piped name was not escaped for its cell:\n%s", md)
	}
	// Every table row must still be a row: a broken cell shows up as a line with
	// too few separators.
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "|") && strings.Count(line, "|") < 3 {
			t.Errorf("row lost its cells: %q", line)
		}
	}
}

// TestMarkdownProseCannotInjectHTML: a reason embeds an os error over a path the
// user does not control. Rendered unescaped, that path becomes markup.
func TestMarkdownProseCannotInjectHTML(t *testing.T) {
	res := Result{
		Host: "0.3.0",
		Entries: []Entry{{
			Name: "dsh-gone", Version: "1.0.0", Status: StatusUnknown,
			Reason: `manifest unreadable: <script>alert(1)</script>`,
		}},
	}
	res.Counts = Counts{Packages: 1, Unknown: 1}
	md := renderMarkdown(t, res)

	if strings.Contains(md, "<script>") {
		t.Errorf("a reason put a raw tag into the summary:\n%s", md)
	}
	if !strings.Contains(md, "&lt;script&gt;") {
		t.Errorf("the tag was neither escaped nor readable:\n%s", md)
	}
}

// TestMarkdownCodeSpanCannotBeClosedByAName: a backtick in a name must not end
// the code span and let the rest of the name render as markdown.
func TestMarkdownCodeSpanCannotBeClosedByAName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "`plain`"},
		{"a`b", "``a`b``"},
		{"`lead", "`` `lead ``"},
		{"trail`", "`` trail` ``"},
		{"a``b", "```a``b```"},
		{"a|b", "`a\\|b`"},
		{"", "—"},
	}
	for _, c := range cases {
		if got := codeSpan(c.in); got != c.want {
			t.Errorf("codeSpan(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestMarkdownReasonNewlineDoesNotSplitTheTable: os errors carry no newline
// today, but a reason is arbitrary text and a single newline would delete every
// row after it — a truncated table reads as a shorter, cleaner report.
func TestMarkdownReasonNewlineDoesNotSplitTheTable(t *testing.T) {
	res := Result{
		Host: "0.3.0",
		Entries: []Entry{
			{Name: "dsh-gone", Version: "1.0.0", Status: StatusUnknown,
				Reason: "manifest unreadable:\n  permission denied"},
		},
	}
	res.Counts = Counts{Packages: 1, Unknown: 1}
	md := renderMarkdown(t, res)

	if strings.Contains(md, "\n  permission denied") {
		t.Errorf("a newline split the unknown table:\n%s", md)
	}
	if !strings.Contains(md, "manifest unreadable: permission denied") {
		t.Errorf("the reason was not flattened:\n%s", md)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("pipe closed") }

// TestMarkdownReturnsWriteError: a step summary truncated halfway still looks
// like a summary, so a write failure has to reach the caller and change the exit
// code rather than being swallowed.
func TestMarkdownReturnsWriteError(t *testing.T) {
	err := Markdown(failWriter{}, Result{Host: "0.3.0"})
	if err == nil {
		t.Fatal("Markdown swallowed a write error")
	}
	if !strings.Contains(err.Error(), "pipe closed") {
		t.Errorf("error = %v, want the writer's error", err)
	}
}
