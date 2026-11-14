// Package docs turns the promise "the README describes what this binary does"
// into something that can fail a test.
//
// A README drifts in three ways that no human review catches reliably:
//
//   - the code table lists a code the tool no longer emits, or omits one it
//     does — the reader then either chases a phantom or meets an unexplained
//     string in their output;
//   - a link or a path points at a file that was moved or deleted, so the
//     "see docs/design.md" trail ends nowhere;
//   - a subcommand exists and is undocumented, which is the same as not
//     shipping it.
//
// Each check is a rule over files, and every check fails closed: if the checker
// cannot read what it is supposed to check, it returns an error rather than an
// empty problem list. A linter that reports "clean" because it could not run is
// worse than no linter, because it is quoted as evidence.
package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/atop0914/dshfit/internal/cli"
	"github.com/atop0914/dshfit/internal/codes"
)

// Problem is one inconsistency. File and Line point at the place to fix, which
// is always the document and never the code: the code is the definition.
type Problem struct {
	File string
	Line int
	Msg  string
}

func (p Problem) String() string {
	if p.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.Msg)
	}
	return fmt.Sprintf("%s: %s", p.File, p.Msg)
}

// CheckRepo runs every rule against the repository rooted at root. The error
// result means the check itself could not run, which is not the same as "the
// documentation is consistent".
func CheckRepo(root string) ([]Problem, error) {
	var probs []Problem
	stages := []func(string) ([]Problem, error){
		CheckCodeTable,
		CheckLocalLinks,
		CheckRepoPaths,
		CheckCommands,
	}
	for _, stage := range stages {
		p, err := stage(root)
		if err != nil {
			return nil, err
		}
		probs = append(probs, p...)
	}
	return probs, nil
}

// markdownFile is one document plus its lines, so a problem can name a line.
type markdownFile struct {
	path  string
	lines []string
}

func readMarkdown(root, rel string) (*markdownFile, error) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	return &markdownFile{path: rel, lines: strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")}, nil
}

// CheckCodeTable compares the README's code table with the registry in
// internal/codes. The two must agree on the set of codes, on each code's
// severity, and on which codes are archive-only — the last one because the
// table says so in prose, and a reader deciding whether a finding applies to
// their checkout relies on it.
func CheckCodeTable(root string) ([]Problem, error) {
	readme, err := readMarkdown(root, "README.md")
	if err != nil {
		return nil, err
	}
	start, err := findTable(readme, func(cells []string) bool {
		return len(cells) >= 3 && cells[0] == "code" && cells[1] == "severity"
	})
	if err != nil {
		return nil, fmt.Errorf("README.md: %w", err)
	}

	rows := readRows(readme, start)
	if len(rows) == 0 {
		return nil, fmt.Errorf("README.md:%d: the code table has no rows", start)
	}

	var probs []Problem
	documented := map[string]bool{}
	for _, row := range rows {
		if len(row.cells) < 3 {
			probs = append(probs, Problem{"README.md", row.line, "code table row needs code, severity and meaning"})
			continue
		}
		id := strings.TrimSpace(strings.Trim(row.cells[0], "`"))
		sev := strings.TrimSpace(row.cells[1])
		meaning := strings.TrimSpace(row.cells[2])

		if documented[id] {
			probs = append(probs, Problem{"README.md", row.line, fmt.Sprintf("%q is documented twice", id)})
			continue
		}
		documented[id] = true

		info, ok := codes.Lookup(id)
		if !ok {
			probs = append(probs, Problem{"README.md", row.line,
				fmt.Sprintf("%q is documented but no code of that name exists", id)})
			continue
		}
		if sev != info.Severity {
			probs = append(probs, Problem{"README.md", row.line,
				fmt.Sprintf("%q is documented as %q, the tool emits %q", id, sev, info.Severity)})
		}
		if meaning == "" {
			probs = append(probs, Problem{"README.md", row.line, fmt.Sprintf("%q has no meaning", id)})
		}
		archiveOnly := strings.Contains(meaning, "(tarball only)")
		if want := info.Scope == codes.Tarball; archiveOnly != want {
			probs = append(probs, Problem{"README.md", row.line, fmt.Sprintf(
				"%q: tarball-only marker is %v, the registry says %v (scope %q)",
				id, archiveOnly, want, info.Scope)})
		}
	}

	var missing []string
	for _, c := range codes.All() {
		if !documented[c.ID] {
			missing = append(missing, c.ID)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		probs = append(probs, Problem{"README.md", start, fmt.Sprintf(
			"the tool can emit %d undocumented code(s): %s", len(missing), strings.Join(missing, ", "))})
	}
	return probs, nil
}

// tableRow is one parsed row with the line it came from.
type tableRow struct {
	line  int
	cells []string
}

// findTable returns the 1-based line of the first row satisfying header.
func findTable(f *markdownFile, header func([]string) bool) (int, error) {
	for i, line := range f.lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		if header(splitRow(line)) {
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("no table header matching the expected columns")
}

// readRows returns the body rows of the table starting at line start, stopping
// at the first line that is not part of it.
func readRows(f *markdownFile, start int) []tableRow {
	var rows []tableRow
	for i := start; i < len(f.lines); i++ { // start is 1-based, so this is the line after the header
		line := strings.TrimSpace(f.lines[i])
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := splitRow(line)
		if isSeparator(cells) {
			continue
		}
		rows = append(rows, tableRow{line: i + 1, cells: cells})
	}
	return rows
}

// splitRow splits a markdown table row into trimmed cells. Escaped pipes (\|)
// are honoured, because a cell that can hold a shell pipe is a cell a reader
// will write one in.
func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")

	var cells []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' && i+1 < len(line) && line[i+1] == '|' {
			cur.WriteByte('|')
			i++
			continue
		}
		if line[i] == '|' {
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(line[i])
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	return cells
}

func isSeparator(cells []string) bool {
	for _, c := range cells {
		if c == "" {
			continue
		}
		if strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return true
}

// linkPattern matches an inline markdown link and captures its target.
var linkPattern = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)

// CheckLocalLinks verifies that every relative link in the repository's
// markdown resolves. External links are not checked: a test that dials the
// network is a test that fails on a bad day for reasons that have nothing to do
// with the code.
func CheckLocalLinks(root string) ([]Problem, error) {
	files, err := markdownFiles(root)
	if err != nil {
		return nil, err
	}
	var probs []Problem
	for _, f := range files {
		for i, line := range f.lines {
			for _, m := range linkPattern.FindAllStringSubmatch(line, -1) {
				target := m[1]
				if isExternal(target) {
					continue
				}
				target = strings.SplitN(target, "#", 2)[0]
				if target == "" {
					continue // a same-page anchor
				}
				full := filepath.Join(filepath.Dir(filepath.Join(root, filepath.FromSlash(f.path))), filepath.FromSlash(target))
				if _, err := os.Stat(full); err != nil {
					probs = append(probs, Problem{f.path, i + 1,
						fmt.Sprintf("link target %q does not exist", target)})
				}
			}
		}
	}
	return probs, nil
}

func isExternal(target string) bool {
	lower := strings.ToLower(target)
	for _, prefix := range []string{"http://", "https://", "mailto:", "tel:"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return strings.HasPrefix(target, "#")
}

// repoRoots are the path prefixes a backticked span must start with before it is
// read as a path claim. Without this list, `package.json` and `lib/*.js` — names
// of files that belong to the *user's* project, not to this one — would be
// checked against this repository and fail.
var repoRoots = []string{"cmd/", "docs/", "internal/", "plugins/", "scripts/", "testdata/", ".github/"}

// CheckRepoPaths verifies that backticked references to this repository's own
// files exist. It is the check that catches a renamed script or fixture still
// being advertised in a document.
func CheckRepoPaths(root string) ([]Problem, error) {
	files, err := markdownFiles(root)
	if err != nil {
		return nil, err
	}
	var probs []Problem
	for _, f := range files {
		for i, line := range f.lines {
			for _, span := range backticked(line) {
				if !claimAboutRepoPath(span) {
					continue
				}
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(span))); err != nil {
					probs = append(probs, Problem{f.path, i + 1,
						fmt.Sprintf("`%s` does not exist in this repository", span)})
				}
			}
		}
	}
	return probs, nil
}

// claimAboutRepoPath decides whether a backticked span is a claim about a file
// here. Wildcards are excluded: `testdata/fuzz/` is a directory a run writes
// into, and a glob is not a path claim that can be answered by os.Stat.
func claimAboutRepoPath(span string) bool {
	if span == "" || strings.ContainsAny(span, "*?<>{} ") {
		return false
	}
	for _, prefix := range repoRoots {
		if strings.HasPrefix(span, prefix) {
			return true
		}
	}
	return false
}

// CheckCommands verifies that every subcommand the binary dispatches is named in
// the README. An undocumented command is a command nobody can find, and the list
// comes from the CLI rather than from a second copy kept here — a copy would
// agree with itself forever.
func CheckCommands(root string) ([]Problem, error) {
	readme, err := readMarkdown(root, "README.md")
	if err != nil {
		return nil, err
	}
	body := strings.Join(readme.lines, "\n")
	var probs []Problem
	for _, cmd := range cli.Subcommands() {
		if !strings.Contains(body, "dshfit "+cmd) {
			probs = append(probs, Problem{"README.md", 0,
				fmt.Sprintf("subcommand %q is never documented as `dshfit %s`", cmd, cmd)})
		}
	}
	return probs, nil
}

func backticked(line string) []string {
	var out []string
	for {
		start := strings.Index(line, "`")
		if start < 0 {
			return out
		}
		rest := line[start+1:]
		end := strings.Index(rest, "`")
		if end < 0 {
			return out
		}
		out = append(out, strings.TrimSpace(rest[:end]))
		line = rest[end+1:]
	}
}

// markdownFiles lists the documents this repository owns. node_modules is
// skipped because a dependency's README is not this project's documentation.
func markdownFiles(root string) ([]*markdownFile, error) {
	var rels []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "bin", ".cache":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".md") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rels = append(rels, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(rels) == 0 {
		return nil, fmt.Errorf("no markdown files under %s", root)
	}
	sort.Strings(rels)
	out := make([]*markdownFile, 0, len(rels))
	for _, rel := range rels {
		f, err := readMarkdown(root, rel)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}
