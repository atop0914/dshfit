package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
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
	return runWithStdin(t, "", args...)
}

func runWithStdin(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(strings.NewReader(stdin), args, &out, &errOut)
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

// makeTarball writes a real .tgz so the tarball path is exercised end to end
// through the same reader the binary uses.
func makeTarball(t *testing.T, entries map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := entries[name]
		if err := tw.WriteHeader(&tar.Header{
			Name: "package/" + name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "dsh-plugin-good-1.0.0.tgz")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCheckAcceptsTarball: the whole point of Day 2 is that a user who only has
// `npm pack` output gets the same verdict as one with node_modules.
func TestCheckAcceptsTarball(t *testing.T) {
	tgz := makeTarball(t, map[string]string{
		"package.json":     goodPkg,
		"cordis.patch.yml": goodPatch,
		"lib/index.js":     "export default 1\n",
	})
	code, stdout, stderr := run(t, "check", "--host", "0.2.0-rc.2", tgz)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s stdout=%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "OK") || !strings.Contains(stdout, "(tarball") {
		t.Errorf("stdout should report an archive-derived verdict:\n%s", stdout)
	}
}

func TestCheckTarballBlocked(t *testing.T) {
	tgz := makeTarball(t, map[string]string{
		"package.json": `{
          "name":"dsh-plugin-stale","version":"1.2.0","files":["lib","cordis.patch.yml"],
          "peerDependencies":{"@deepseek-ai/dsh-app-boot":"^0.1.0-rc.6"},
          "dsh":{"bundle":{"patch":"./cordis.patch.yml"}}}`,
		"cordis.patch.yml": goodPatch,
		"lib/index.js":     "x",
	})
	code, stdout, _ := run(t, "check", "--host", "0.2.0-rc.2", tgz)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	for _, want := range []string{"BLOCKED", "peer-range-excludes-host", "prerelease"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestCheckTarballDeclaredFileMissing(t *testing.T) {
	tgz := makeTarball(t, map[string]string{
		"package.json":     strings.Replace(goodPkg, `"lib", `, `"lib", "docs", `, 1),
		"cordis.patch.yml": goodPatch,
	})
	code, stdout, _ := run(t, "check", "--host", "0.2.0-rc.2", tgz)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stdout, "declared-file-not-in-tarball") {
		t.Errorf("stdout missing the archive cross-check:\n%s", stdout)
	}
}

// TestCheckTarballUnreadableIsAReportNotACrash keeps the mixed-argument contract:
// one bad archive must not hide the verdict for everything else in the run.
func TestCheckTarballUnreadableIsAReportNotACrash(t *testing.T) {
	dir := writePlugin(t, map[string]string{
		"package.json":     goodPkg,
		"cordis.patch.yml": goodPatch,
	})
	bad := filepath.Join(t.TempDir(), "corrupt.tgz")
	if err := os.WriteFile(bad, []byte("not gzip at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := run(t, "check", "--host", "0.2.0-rc.2", dir, bad)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stdout, "manifest-unreadable") {
		t.Errorf("the corrupt archive should be a report:\n%s", stdout)
	}
	if !strings.Contains(stdout, "1 ok, 0 warn, 1 blocked") {
		t.Errorf("the good plugin should still be reported:\n%s", stdout)
	}
}

func TestCheckJSONCarriesSource(t *testing.T) {
	tgz := makeTarball(t, map[string]string{
		"package.json":     goodPkg,
		"cordis.patch.yml": goodPatch,
		"lib/index.js":     "x",
	})
	code, stdout, stderr := run(t, "check", "--host", "0.2.0-rc.2", "--json", tgz)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr)
	}
	var reports []compat.Report
	if err := json.Unmarshal([]byte(stdout), &reports); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout)
	}
	if len(reports) != 1 {
		t.Fatalf("got %d reports", len(reports))
	}
	if reports[0].Source != "tarball" {
		t.Errorf("source = %q, want tarball", reports[0].Source)
	}
	if !strings.HasSuffix(reports[0].Dir, ".tgz") {
		t.Errorf("dir should be the archive path, got %q", reports[0].Dir)
	}
}
