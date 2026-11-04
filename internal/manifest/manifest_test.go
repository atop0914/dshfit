package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, files map[string]string) string {
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

func TestLoad(t *testing.T) {
	dir := write(t, map[string]string{"package.json": `{
      "name": "dsh-plugin-x",
      "version": "1.2.3",
      "files": ["lib", "cordis.patch.yml"],
      "peerDependencies": {"@deepseek-ai/dsh": "^0.2.0", "react": "^19.0.0"},
      "engines": {"node": "^22.19 || >=24", "dsh": ">=0.2.0-rc.2"},
      "dsh": {"bundle": {"patch": "./cordis.patch.yml"}}
    }`})
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "dsh-plugin-x" || p.Version != "1.2.3" {
		t.Fatalf("got %+v", p)
	}
	if p.Bundle == nil || p.Bundle.Patch != "./cordis.patch.yml" {
		t.Fatalf("bundle = %+v", p.Bundle)
	}
	if got := p.Engines["dsh"]; got != ">=0.2.0-rc.2" {
		t.Fatalf("engines.dsh = %q", got)
	}
	// react is a peer, but not a DSH package: it must not be evaluated against
	// the harness version.
	names := p.PeerNames()
	if len(names) != 1 || names[0] != "@deepseek-ai/dsh" {
		t.Fatalf("PeerNames() = %v", names)
	}
}

func TestLoadRejectsNameless(t *testing.T) {
	dir := write(t, map[string]string{"package.json": `{"version":"1.0.0"}`})
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for a manifest without a name")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("expected error for a directory without package.json")
	}
}

func TestBundlePatches(t *testing.T) {
	patch := `# leading comment
- remove:
    - id: dsh-plugin-old
- insert:
    - id: dsh-plugin-y   # trailing comment
      name: "dsh-plugin-y"
    - id: dsh-plugin-y-extra
      name: dsh-plugin-y-extra
`
	dir := write(t, map[string]string{
		"package.json":     `{"name":"dsh-plugin-y","version":"1.0.0","dsh":{"bundle":{"patch":"cordis.patch.yml"}}}`,
		"cordis.patch.yml": patch,
	})
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := p.BundlePatches()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries: %+v", len(entries), entries)
	}
	if entries[0].ID != "dsh-plugin-y" || entries[0].Name != "dsh-plugin-y" {
		t.Errorf("entry 0 = %+v", entries[0])
	}
	if entries[1].ID != "dsh-plugin-y-extra" {
		t.Errorf("entry 1 = %+v", entries[1])
	}
}

func TestBundlePatchesRequiresInsertBlock(t *testing.T) {
	dir := write(t, map[string]string{
		"package.json":     `{"name":"dsh-plugin-z","version":"1.0.0","dsh":{"bundle":{"patch":"cordis.patch.yml"}}}`,
		"cordis.patch.yml": "- remove:\n    - id: gone\n",
	})
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.BundlePatches(); err == nil {
		t.Fatal("expected an error when the patch contributes no inserts")
	}
}

func TestBundlePatchesNeedsBundle(t *testing.T) {
	dir := write(t, map[string]string{"package.json": `{"name":"dsh-plugin-w","version":"1.0.0"}`})
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.BundlePatches(); err == nil {
		t.Fatal("expected an error when dsh.bundle is absent")
	}
}
