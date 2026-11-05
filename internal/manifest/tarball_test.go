package manifest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// archive is one entry to write into a real tarball. typ is a tar.Typeflag;
// zero means "regular file".
type archive struct {
	name string
	body string
	typ  byte
}

// makeTarball writes a genuinely gzipped, genuinely tarred archive to a temp
// file. The tests read it back through the same code path a user's `npm pack`
// output takes, so a bug in either archive package shows up as a failure rather
// than as a mock agreement.
func makeTarball(t *testing.T, name string, entries ...archive) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Typeflag: typ}
		switch typ {
		case tar.TypeReg:
			hdr.Size = int64(len(e.body))
		case tar.TypeDir:
			hdr.Mode = 0o755
		case tar.TypeSymlink:
			hdr.Linkname = e.body
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Size > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const tarPkg = `{"name":"dsh-plugin-tar","version":"1.2.3",
  "files":["lib","cordis.patch.yml"],
  "peerDependencies":{"@deepseek-ai/dsh-app-boot":"^0.2.0-rc.2"},
  "dsh":{"bundle":{"patch":"./cordis.patch.yml"}}}`

const tarPatch = "- insert:\n    - id: dsh-plugin-tar\n      name: dsh-plugin-tar\n"

func tarEntries() []archive {
	// npm's tarballs carry directory entries as well as files; the reader has to
	// handle both, so the fixture carries both.
	return []archive{
		{name: "package/", typ: tar.TypeDir},
		{name: "package/package.json", body: tarPkg},
		{name: "package/cordis.patch.yml", body: tarPatch},
		{name: "package/lib/", typ: tar.TypeDir},
		{name: "package/lib/index.js", body: "export default 1\n"},
		{name: "package/lib/extra/", typ: tar.TypeDir},
		{name: "package/lib/extra/deep.js", body: "export default 2\n"},
	}
}

func TestLoadTarballReadsPackageAndPatch(t *testing.T) {
	p, err := LoadTarball(makeTarball(t, "dsh-plugin-tar-1.2.3.tgz", tarEntries()...))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "dsh-plugin-tar" || p.Version != "1.2.3" {
		t.Fatalf("got %+v", p)
	}
	if p.Source != TarballSource {
		t.Errorf("Source = %q, want %q", p.Source, TarballSource)
	}
	if p.Dir != "" {
		t.Errorf("a tarball has no directory, got %q", p.Dir)
	}
	if p.Location() != p.Tarball {
		t.Errorf("Location() = %q, want the tarball path %q", p.Location(), p.Tarball)
	}
	// Entries are package-relative, the package/ prefix is gone, and the list is
	// sorted so two runs print the same thing.
	want := []string{"cordis.patch.yml", "lib", "lib/extra", "lib/extra/deep.js", "lib/index.js", "package.json"}
	if strings.Join(p.Entries, ",") != strings.Join(want, ",") {
		t.Fatalf("Entries = %v, want %v", p.Entries, want)
	}
	// The patch is read out of the archive, not off disk: there is no directory
	// to read it from.
	rows, err := p.BundlePatches()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "dsh-plugin-tar" {
		t.Fatalf("rows = %+v", rows)
	}
	if !p.Contains("cordis.patch.yml") {
		t.Error("Contains should see a file that is in the archive")
	}
	if p.Contains("lib/missing.js") {
		t.Error("Contains must not invent a file")
	}
}

// TestLoadTarballRequiresNPMRoot: a different layout cannot be repaired by
// guessing which segment was the root, so it is an error rather than a verdict.
func TestLoadTarballRequiresNPMRoot(t *testing.T) {
	file := makeTarball(t, "flat.tgz",
		archive{name: "package.json", body: tarPkg},
		archive{name: "cordis.patch.yml", body: tarPatch},
	)
	_, err := LoadTarball(file)
	if err == nil {
		t.Fatal("expected an error for an archive with no package/ prefix")
	}
	if !strings.Contains(err.Error(), "package/") {
		t.Errorf("error should name the expected root, got %q", err)
	}
}

// TestLoadTarballRefusesEscapingEntries pins the safety property. The unsafe
// entry is reported, never parsed, and the safe entries still produce a verdict:
// refusing the whole archive would hide which plugin it was.
func TestLoadTarballRefusesEscapingEntries(t *testing.T) {
	file := makeTarball(t, "evil.tgz",
		archive{name: "package/package.json", body: tarPkg},
		archive{name: "package/cordis.patch.yml", body: tarPatch},
		archive{name: "package/../evil.txt", body: "pwned"},
	)
	p, err := LoadTarball(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Unsafe) != 1 {
		t.Fatalf("Unsafe = %+v, want exactly the escaping entry", p.Unsafe)
	}
	if p.Unsafe[0].Path != "package/../evil.txt" {
		t.Errorf("unsafe path = %q", p.Unsafe[0].Path)
	}
	if p.Unsafe[0].Reason != ReasonOutside {
		t.Errorf("reason = %q, want %q", p.Unsafe[0].Reason, ReasonOutside)
	}
	for _, e := range p.Entries {
		if strings.Contains(e, "..") || strings.Contains(e, "evil") {
			t.Fatalf("a refused entry leaked into Entries: %v", p.Entries)
		}
	}
}

func TestLoadTarballRefusesAbsolutePath(t *testing.T) {
	file := makeTarball(t, "abs.tgz",
		archive{name: "package/package.json", body: tarPkg},
		archive{name: "/etc/passwd", body: "root:x:0:0"},
	)
	p, err := LoadTarball(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Unsafe) != 1 || p.Unsafe[0].Reason != ReasonOutside {
		t.Fatalf("Unsafe = %+v", p.Unsafe)
	}
}

func TestLoadTarballRefusesSymlink(t *testing.T) {
	file := makeTarball(t, "link.tgz",
		archive{name: "package/package.json", body: tarPkg},
		archive{name: "package/link", body: "../../etc/passwd", typ: tar.TypeSymlink},
	)
	p, err := LoadTarball(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Unsafe) != 1 {
		t.Fatalf("Unsafe = %+v", p.Unsafe)
	}
	if p.Unsafe[0].Reason != ReasonLink {
		t.Errorf("reason = %q, want %q", p.Unsafe[0].Reason, ReasonLink)
	}
	if p.Contains("link") {
		t.Error("a refused link must not count as a shipped file")
	}
}

// TestLoadTarballRejectsEmptyFile: a truncated download is the most common bad
// input in practice, and it must not read as "a package with no problems".
func TestLoadTarballRejectsEmptyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.tgz")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTarball(p); err == nil {
		t.Fatal("expected an error for a 0-byte file")
	}
}

func TestLoadTarballRejectsTruncatedGzip(t *testing.T) {
	file := makeTarball(t, "full.tgz", tarEntries()...)
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	cut := filepath.Join(t.TempDir(), "cut.tgz")
	if err := os.WriteFile(cut, body[:len(body)/2], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTarball(cut); err == nil {
		t.Fatal("expected an error for a truncated archive")
	}
}

func TestLoadTarballRequiresPackageJSON(t *testing.T) {
	file := makeTarball(t, "nopkg.tgz", archive{name: "package/lib/index.js", body: "x"})
	if _, err := LoadTarball(file); err == nil {
		t.Fatal("expected an error when package.json is not in the archive")
	}
}

func TestLoadTarballMissingPatchIsAnError(t *testing.T) {
	file := makeTarball(t, "nopatch.tgz",
		archive{name: "package/package.json", body: tarPkg},
	)
	p, err := LoadTarball(file)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.BundlePatches()
	if err == nil {
		t.Fatal("expected an error for a patch that is not in the archive")
	}
	if !strings.Contains(err.Error(), "not in the tarball") {
		t.Errorf("error should say the patch is absent from the archive, got %q", err)
	}
}

// TestLoadPathSniffsContentFirst: npm pack output is routinely renamed, so the
// bytes decide before the extension does.
func TestLoadPathSniffsContentFirst(t *testing.T) {
	file := makeTarball(t, "renamed.bin", tarEntries()...)
	p, err := LoadPath(file)
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != TarballSource {
		t.Fatalf("Source = %q, want tarball", p.Source)
	}
}

func TestLoadPathRejectsUnknownFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(p, []byte("not a package"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPath(p); err == nil {
		t.Fatal("expected an error for a plain file")
	}
}

// TestLoadPathWithTGZSuffixButNoMagic: the suffix routes it to the archive
// reader, which reports the real problem instead of pretending it is a directory.
func TestLoadPathWithTGZSuffixButNoMagic(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bogus.tgz")
	if err := os.WriteFile(p, []byte("PK\x03\x04zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadPath(p)
	if err == nil {
		t.Fatal("expected an error for a .tgz that is not gzip")
	}
	if !strings.Contains(err.Error(), "gzip") {
		t.Errorf("error should mention gzip, got %q", err)
	}
}

func TestLoadPathReadsDirectory(t *testing.T) {
	dir := write(t, map[string]string{"package.json": tarPkg, "cordis.patch.yml": tarPatch})
	p, err := LoadPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != DirSource {
		t.Fatalf("Source = %q, want dir", p.Source)
	}
	if p.Entries != nil {
		t.Errorf("a directory has no archive entries, got %v", p.Entries)
	}
}
