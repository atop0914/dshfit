package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atop0914/dshfit/internal/compat"
)

func writePlugin(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const goodPkg = `{
  "name": "dsh-plugin-good",
  "version": "1.0.0",
  "files": ["lib", "cordis.patch.yml"],
  "peerDependencies": {"@deepseek-ai/dsh-app-boot": "^0.2.0-rc.2"},
  "dsh": {"bundle": {"patch": "./cordis.patch.yml"}}
}`

const goodPatch = "- insert:\n    - id: dsh-plugin-good\n      name: dsh-plugin-good\n"

const stalePkg = `{
  "name": "dsh-plugin-stale",
  "version": "1.2.0",
  "files": ["lib", "cordis.patch.yml"],
  "peerDependencies": {"@deepseek-ai/dsh-app-boot": "^0.1.0-rc.6"},
  "dsh": {"bundle": {"patch": "./cordis.patch.yml"}}
}`

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestNoArgs(t *testing.T) {
	code, _, stderr := run(t)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "usage:") {
		t.Errorf("stderr should show usage, got %q", stderr)
	}
}

func TestCheckRequiresHost(t *testing.T) {
	dir := writePlugin(t, map[string]string{"package.json": goodPkg})
	code, _, stderr := run(t, "check", dir)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--host is required") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestCheckCompatibleExitsZero(t *testing.T) {
	dir := writePlugin(t, map[string]string{
		"package.json":     goodPkg,
		"cordis.patch.yml": goodPatch,
	})
	code, stdout, stderr := run(t, "check", "--host", "0.2.0-rc.2", dir)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "OK") {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stdout, "1 ok, 0 warn, 0 blocked") {
		t.Errorf("summary missing: %q", stdout)
	}
}

// TestCheckBlockedExitsOne is the CI contract: a script can gate an upgrade on
// the exit code alone.
func TestCheckBlockedExitsOne(t *testing.T) {
	dir := writePlugin(t, map[string]string{
		"package.json":     stalePkg,
		"cordis.patch.yml": goodPatch,
	})
	code, stdout, _ := run(t, "check", "--host", "0.2.0-rc.2", dir)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	for _, want := range []string{"BLOCKED", "peer-range-excludes-host", "^0.1.0-rc.6", "prerelease"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestCheckJSON(t *testing.T) {
	dir := writePlugin(t, map[string]string{
		"package.json":     stalePkg,
		"cordis.patch.yml": goodPatch,
	})
	code, stdout, stderr := run(t, "check", "--host", "0.2.0-rc.2", "--json", dir)
	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr=%s", code, stderr)
	}
	var reports []compat.Report
	if err := json.Unmarshal([]byte(stdout), &reports); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout)
	}
	if len(reports) != 1 {
		t.Fatalf("got %d reports", len(reports))
	}
	if reports[0].Verdict != compat.Blocked {
		t.Errorf("verdict = %q", reports[0].Verdict)
	}
	if reports[0].Host != "0.2.0-rc.2" {
		t.Errorf("host = %q", reports[0].Host)
	}
}

// TestUnreadablePluginDoesNotStopTheRun covers the realistic profile: one broken
// checkout must not hide the verdict for everything else.
func TestUnreadablePluginDoesNotStopTheRun(t *testing.T) {
	good := writePlugin(t, map[string]string{
		"package.json":     goodPkg,
		"cordis.patch.yml": goodPatch,
	})
	empty := t.TempDir()

	code, stdout, _ := run(t, "check", "--host", "0.2.0-rc.2", good, empty)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stdout, "manifest-unreadable") {
		t.Errorf("stdout missing manifest-unreadable:\n%s", stdout)
	}
	if !strings.Contains(stdout, "OK") || !strings.Contains(stdout, "1 ok, 0 warn, 1 blocked") {
		t.Errorf("the good plugin should still be reported:\n%s", stdout)
	}
}

func TestVersionCommand(t *testing.T) {
	code, stdout, _ := run(t, "version")
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "dshfit") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, stderr := run(t, "frobnicate")
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Errorf("stderr = %q", stderr)
	}
}
