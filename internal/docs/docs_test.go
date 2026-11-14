package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRepositoryDocumentationIsConsistent is the gate itself: it runs every rule
// against this repository. It reads README.md, docs/design.md and the plugin's
// README, so a renamed document or a dropped code row fails here rather than in
// a reader's terminal.
func TestRepositoryDocumentationIsConsistent(t *testing.T) {
	probs, err := CheckRepo(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("the check could not run: %v", err)
	}
	for _, p := range probs {
		t.Errorf("%s", p)
	}
}

// TestCheckFailsClosed is the lesson from a previous project's checker: a
// checker that reports "clean" because it could not open the file is quoted as
// evidence that the documentation is fine. Every "cannot run" case has to be an
// error, not an empty result.
func TestCheckFailsClosed(t *testing.T) {
	root := t.TempDir()
	if _, err := CheckCodeTable(root); err == nil {
		t.Error("a missing README must be an error, not a clean result")
	}
	if _, err := CheckLocalLinks(root); err == nil {
		t.Error("no markdown at all must be an error, not a clean result")
	}
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func problemsMention(probs []Problem, want string) bool {
	for _, p := range probs {
		if strings.Contains(p.Msg, want) {
			return true
		}
	}
	return false
}

// TestCodeTableProblemsAreCaught feeds the checker documents that are wrong in
// each of the four ways that matter, one at a time.
func TestCodeTableProblemsAreCaught(t *testing.T) {
	cases := []struct {
		name string
		rows string
		want string
	}{
		{
			name: "unknown code",
			rows: "| `no-such-code` | block | invented |\n",
			want: "no code of that name exists",
		},
		{
			name: "wrong severity",
			rows: "| `no-bundle` | warn | no bundle |\n",
			want: "the tool emits \"block\"",
		},
		{
			name: "tarball marker missing",
			rows: "| `tarball-entry-unsafe` | block | a link entry |\n",
			want: "tarball-only marker is false",
		},
		{
			name: "tarball marker on a directory code",
			rows: "| `no-bundle` | block | no bundle *(tarball only)* |\n",
			want: "tarball-only marker is true",
		},
		{
			name: "duplicate row",
			rows: "| `no-bundle` | block | no bundle |\n| `no-bundle` | block | again |\n",
			want: "documented twice",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "README.md", "| code | severity | meaning |\n|---|---|---|\n"+tc.rows)
			probs, err := CheckCodeTable(root)
			if err != nil {
				t.Fatalf("CheckCodeTable: %v", err)
			}
			if !problemsMention(probs, tc.want) {
				t.Fatalf("problems = %v, want one mentioning %q", probs, tc.want)
			}
		})
	}
}

// TestUndocumentedCodeIsReported is the reverse direction: the table can be
// perfectly formed and still omit a code the tool emits. That is the failure
// that reaches a user as an unexplained string.
func TestUndocumentedCodeIsReported(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md",
		"| code | severity | meaning |\n|---|---|---|\n| `no-bundle` | block | x |\n")
	probs, err := CheckCodeTable(root)
	if err != nil {
		t.Fatalf("CheckCodeTable: %v", err)
	}
	if !problemsMention(probs, "undocumented code") {
		t.Fatalf("problems = %v, want the missing codes to be listed", probs)
	}
}

func TestBrokenLinkAndMissingPathAreReported(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", ""+
		"See [the design](docs/design.md) and [the site](https://example.com).\n"+
		"Run `scripts/readme-rehearsal.sh`.\n")
	if probs, err := CheckLocalLinks(root); err != nil || !problemsMention(probs, "does not exist") {
		t.Fatalf("CheckLocalLinks = (%v, %v), want a missing-link problem", probs, err)
	}
	if probs, err := CheckRepoPaths(root); err != nil || !problemsMention(probs, "scripts/readme-rehearsal.sh") {
		t.Fatalf("CheckRepoPaths = (%v, %v), want a missing-path problem", probs, err)
	}
}

// TestUserProjectPathsAreNotJudged pins the line the path rule draws: this
// repository's documents talk about files that live in the *user's* project
// (`package.json`, `lib/*.js`). Checking those against this tree would make the
// rule fail on every correct sentence.
func TestUserProjectPathsAreNotJudged(t *testing.T) {
	for _, span := range []string{"package.json", "cordis.patch.yml", "lib/*.js", "~/.dsh/profiles"} {
		if claimAboutRepoPath(span) {
			t.Errorf("%q was read as a claim about a file in this repository", span)
		}
	}
	for _, span := range []string{"scripts/pre-commit.sh", "testdata/profile/web/list.json", "docs/design.md"} {
		if !claimAboutRepoPath(span) {
			t.Errorf("%q should be read as a claim about this repository", span)
		}
	}
}

func TestSubcommandsAreDocumented(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "```console\n$ dshfit check --host 1.2.3 .\n```\n")
	probs, err := CheckCommands(root)
	if err != nil {
		t.Fatalf("CheckCommands: %v", err)
	}
	if len(probs) == 0 {
		t.Fatal("a README documenting only `check` must report the other subcommands")
	}
	if !problemsMention(probs, "serve") {
		t.Errorf("problems = %v, want serve among the missing", probs)
	}
}
