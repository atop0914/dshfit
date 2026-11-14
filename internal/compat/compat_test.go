package compat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atop0914/dshfit/internal/manifest"
)

func writePlugin(t *testing.T, pkg string, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range extra {
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

func issueCodes(rep Report) []string {
	out := make([]string, 0, len(rep.Issues))
	for _, is := range rep.Issues {
		out = append(out, is.Code)
	}
	return out
}

func has(rep Report, code string) bool {
	for _, is := range rep.Issues {
		if is.Code == code {
			return true
		}
	}
	return false
}

const goodPkg = `{
  "name": "dsh-plugin-good",
  "version": "1.0.0",
  "type": "module",
  "main": "lib/index.js",
  "files": ["lib", "cordis.patch.yml", "README.md"],
  "peerDependencies": {"@deepseek-ai/dsh-app-boot": "^0.2.0-rc.2"},
  "engines": {"dsh": ">=0.2.0-rc.2 <0.3.0"},
  "dsh": {"bundle": {"patch": "./cordis.patch.yml"}}
}`

const goodPatch = `# bundle contributed by dsh-plugin-good
- insert:
    - id: dsh-plugin-good
      name: dsh-plugin-good
      config:
        port: 3080
`

func load(t *testing.T, dir string) *manifest.Plugin {
	t.Helper()
	p, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("manifest.Load: %v", err)
	}
	return p
}

func TestCompatiblePlugin(t *testing.T) {
	dir := writePlugin(t, goodPkg, map[string]string{"cordis.patch.yml": goodPatch})
	rep, err := Evaluate(load(t, dir), "0.2.0-rc.2")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != OK {
		t.Fatalf("verdict = %q, want ok; issues: %v", rep.Verdict, rep.Issues)
	}
	if len(rep.Issues) != 0 {
		t.Fatalf("unexpected issues: %v", rep.Issues)
	}
}

// TestZeroOnePeerBlocksZeroTwoHost is the headline case: the plugin itself is
// perfectly well formed, and it still cannot load, because of a semver range.
func TestZeroOnePeerBlocksZeroTwoHost(t *testing.T) {
	pkg := strings.Replace(goodPkg, `"^0.2.0-rc.2"`, `"^0.1.0-rc.6"`, 1)
	pkg = strings.Replace(pkg, `">=0.2.0-rc.2 <0.3.0"`, `">=0.1.0-rc.6 <0.2.0"`, 1)
	dir := writePlugin(t, pkg, map[string]string{"cordis.patch.yml": goodPatch})

	rep, err := Evaluate(load(t, dir), "0.2.0-rc.2")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != Blocked {
		t.Fatalf("verdict = %q, want blocked", rep.Verdict)
	}
	if !has(rep, "peer-range-excludes-host") {
		t.Fatalf("codes = %v, want peer-range-excludes-host", issueCodes(rep))
	}
	for _, is := range rep.Issues {
		if is.Code != "peer-range-excludes-host" {
			continue
		}
		if !strings.Contains(is.Detail, "prerelease") {
			t.Errorf("detail should explain the prerelease gate, got %q", is.Detail)
		}
		if is.Package != "@deepseek-ai/dsh-app-boot" {
			t.Errorf("issue should name the peer, got %q", is.Package)
		}
	}
}

func TestMissingBundleInstallsButNeverMounts(t *testing.T) {
	pkg := `{"name":"dsh-plugin-nobundle","version":"1.0.0","files":["lib"]}`
	dir := writePlugin(t, pkg, nil)
	rep, err := Evaluate(load(t, dir), "0.2.0-rc.2")
	if err != nil {
		t.Fatal(err)
	}
	if !has(rep, "no-bundle") {
		t.Fatalf("codes = %v, want no-bundle", issueCodes(rep))
	}
	if rep.Verdict != Blocked {
		t.Fatalf("verdict = %q, want blocked", rep.Verdict)
	}
}

func TestPatchMissingFromFiles(t *testing.T) {
	pkg := `{
      "name":"dsh-plugin-omits-patch","version":"1.0.0","files":["lib"],
      "dsh":{"bundle":{"patch":"./cordis.patch.yml"}}
    }`
	dir := writePlugin(t, pkg, map[string]string{"cordis.patch.yml": goodPatch})
	rep, err := Evaluate(load(t, dir), "0.2.0-rc.2")
	if err != nil {
		t.Fatal(err)
	}
	if !has(rep, "patch-missing-from-files") {
		t.Fatalf("codes = %v, want patch-missing-from-files", issueCodes(rep))
	}
}

func TestPatchNameMismatch(t *testing.T) {
	patch := "- insert:\n    - id: something-else\n      name: dsh-plugin-other\n"
	dir := writePlugin(t, goodPkg, map[string]string{"cordis.patch.yml": patch})
	rep, err := Evaluate(load(t, dir), "0.2.0-rc.2")
	if err != nil {
		t.Fatal(err)
	}
	if !has(rep, "patch-name-mismatch") {
		t.Fatalf("codes = %v, want patch-name-mismatch", issueCodes(rep))
	}
}

// TestEnginesOnlyWarns pins the severity choice. engines.dsh is documented as a
// declaration that nothing enforces, so blocking on it would reject plugins that
// load fine; the warning still surfaces the author's stated support window.
func TestEnginesOnlyWarns(t *testing.T) {
	pkg := strings.Replace(goodPkg, `">=0.2.0-rc.2 <0.3.0"`, `">=0.1.0 <0.2.0"`, 1)
	dir := writePlugin(t, pkg, map[string]string{"cordis.patch.yml": goodPatch})
	rep, err := Evaluate(load(t, dir), "0.2.0-rc.2")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != Warning {
		t.Fatalf("verdict = %q, want warn; issues: %v", rep.Verdict, rep.Issues)
	}
	if !has(rep, "engines-dsh-excludes-host") {
		t.Fatalf("codes = %v, want engines-dsh-excludes-host", issueCodes(rep))
	}
}

func TestBadHostVersion(t *testing.T) {
	dir := writePlugin(t, goodPkg, map[string]string{"cordis.patch.yml": goodPatch})
	if _, err := Evaluate(load(t, dir), "0.2"); err == nil {
		t.Fatal("expected error for a partial host version")
	}
}
