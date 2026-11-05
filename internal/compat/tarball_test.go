package compat

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atop0914/dshfit/internal/manifest"
)

type archive struct {
	name string
	body string
	typ  byte
}

// makeTarball writes a real gzipped tar so these tests exercise the same reader
// the binary uses. A hand-built struct would let a change in the archive reader
// pass unnoticed.
func makeTarball(t *testing.T, entries ...archive) string {
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
	p := filepath.Join(t.TempDir(), "pkg.tgz")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func loadTar(t *testing.T, file string) *manifest.Plugin {
	t.Helper()
	p, err := manifest.LoadPath(file)
	if err != nil {
		t.Fatalf("manifest.LoadPath(%s): %v", file, err)
	}
	return p
}

func tarPkgWith(files string) string {
	return `{
      "name":"dsh-plugin-t","version":"1.0.0","files":` + files + `,
      "peerDependencies":{"@deepseek-ai/dsh-app-boot":"^0.2.0-rc.2"},
      "dsh":{"bundle":{"patch":"./cordis.patch.yml"}}
    }`
}

const tarPatch = "- insert:\n    - id: dsh-plugin-t\n      name: dsh-plugin-t\n"

func goodTar(t *testing.T, files string) string {
	t.Helper()
	return makeTarball(t,
		archive{name: "package/", typ: tar.TypeDir},
		archive{name: "package/package.json", body: tarPkgWith(files)},
		archive{name: "package/cordis.patch.yml", body: tarPatch},
		archive{name: "package/lib/index.js", body: "export default 1\n"},
	)
}

func TestTarballHappyPathIsSilent(t *testing.T) {
	rep, err := Evaluate(loadTar(t, goodTar(t, `["lib","cordis.patch.yml"]`)), "0.2.0-rc.2")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != OK {
		t.Fatalf("verdict = %q, want ok; issues: %v", rep.Verdict, rep.Issues)
	}
	if rep.Source != string(manifest.TarballSource) {
		t.Errorf("source = %q", rep.Source)
	}
	if rep.Dir == "" {
		t.Error("dir should point at the tarball")
	}
}

// TestDeclaredFileNotInTarball is the finding that a directory cannot produce:
// the manifest promises a path and the artifact does not contain it.
func TestDeclaredFileNotInTarball(t *testing.T) {
	rep, err := Evaluate(loadTar(t, goodTar(t, `["lib","cordis.patch.yml","docs"]`)), "0.2.0-rc.2")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != Blocked {
		t.Fatalf("verdict = %q, want blocked; issues: %v", rep.Verdict, rep.Issues)
	}
	if !has(rep, "declared-file-not-in-tarball") {
		t.Fatalf("codes = %v", codes(rep))
	}
	for _, is := range rep.Issues {
		if is.Code == "declared-file-not-in-tarball" && is.Range != "docs" {
			t.Errorf("issue should name the declared path, got %q", is.Range)
		}
	}
}

// TestTarballModeIsNotDirectoryMode is the reverse assertion that keeps this
// feature from becoming a false-positive machine: a directory checkout has no
// archive, so archive findings must be absent, not merely empty.
func TestTarballModeIsNotDirectoryMode(t *testing.T) {
	// docs is declared but does not exist on disk, and there is no tarball to
	// prove anything either way.
	dir := writePlugin(t, tarPkgWith(`["lib","cordis.patch.yml","docs"]`),
		map[string]string{"cordis.patch.yml": tarPatch, "lib/index.js": "x"})
	rep, err := Evaluate(load(t, dir), "0.2.0-rc.2")
	if err != nil {
		t.Fatal(err)
	}
	for _, is := range rep.Issues {
		switch is.Code {
		case "declared-file-not-in-tarball", "tarball-entry-outside-package", "tarball-entry-unsafe":
			t.Fatalf("directory input must never report %q", is.Code)
		}
	}
	if rep.Verdict != OK {
		t.Fatalf("verdict = %q, want ok; issues: %v", rep.Verdict, rep.Issues)
	}
	if rep.Source != string(manifest.DirSource) {
		t.Errorf("source = %q", rep.Source)
	}
}

func TestTarballEntryOutsidePackage(t *testing.T) {
	file := makeTarball(t,
		archive{name: "package/package.json", body: tarPkgWith(`["cordis.patch.yml"]`)},
		archive{name: "package/cordis.patch.yml", body: tarPatch},
		archive{name: "package/../../etc/cron.d/x", body: "* * * * * root sh"},
	)
	rep, err := Evaluate(loadTar(t, file), "0.2.0-rc.2")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != Blocked || !has(rep, "tarball-entry-outside-package") {
		t.Fatalf("verdict = %q, codes = %v", rep.Verdict, codes(rep))
	}
}

func TestTarballEntrySymlink(t *testing.T) {
	file := makeTarball(t,
		archive{name: "package/package.json", body: tarPkgWith(`["cordis.patch.yml"]`)},
		archive{name: "package/cordis.patch.yml", body: tarPatch},
		archive{name: "package/escape", body: "/etc", typ: tar.TypeSymlink},
	)
	rep, err := Evaluate(loadTar(t, file), "0.2.0-rc.2")
	if err != nil {
		t.Fatal(err)
	}
	if !has(rep, "tarball-entry-unsafe") {
		t.Fatalf("codes = %v", codes(rep))
	}
	var seen bool
	for _, is := range rep.Issues {
		if is.Code == "tarball-entry-unsafe" {
			seen = true
			if !strings.Contains(is.Detail, "package/escape") {
				t.Errorf("detail should name the entry, got %q", is.Detail)
			}
		}
		if is.Code == "tarball-entry-outside-package" {
			t.Errorf("a link inside package/ is not a path escape: %v", rep.Issues)
		}
	}
	if !seen || rep.Verdict != Blocked {
		t.Fatalf("verdict = %q, codes = %v", rep.Verdict, codes(rep))
	}
}

// TestTarballProvesStaleFilesEntryHarmless: files[] omits the patch, but the
// archive demonstrably carries it. Predicting a block here would be wrong, and
// a tool that blocks a package it can see works teaches users to ignore it.
func TestTarballProvesStaleFilesEntryHarmless(t *testing.T) {
	rep, err := Evaluate(loadTar(t, goodTar(t, `["lib"]`)), "0.2.0-rc.2")
	if err != nil {
		t.Fatal(err)
	}
	if has(rep, "patch-missing-from-files") {
		t.Fatalf("the patch is in the archive, so it cannot be missing: %v", rep.Issues)
	}
	if rep.Verdict != OK {
		t.Fatalf("verdict = %q", rep.Verdict)
	}
}

func TestTarballPatchDeclaredButNotPacked(t *testing.T) {
	file := makeTarball(t,
		archive{name: "package/package.json", body: tarPkgWith(`["lib","cordis.patch.yml"]`)},
		archive{name: "package/lib/index.js", body: "x"},
	)
	rep, err := Evaluate(loadTar(t, file), "0.2.0-rc.2")
	if err != nil {
		t.Fatal(err)
	}
	if !has(rep, "declared-file-not-in-tarball") {
		t.Fatalf("codes = %v, want declared-file-not-in-tarball", codes(rep))
	}
	if !has(rep, "patch-unreadable") {
		t.Fatalf("codes = %v, want patch-unreadable from the same cause", codes(rep))
	}
	if rep.Verdict != Blocked {
		t.Fatalf("verdict = %q", rep.Verdict)
	}
}

// TestTarballGlobsAreNotJudged: a pattern is not a path, and answering "missing"
// for one would block packages that are fine. Refusing to answer is the only
// honest option.
func TestTarballGlobsAreNotJudged(t *testing.T) {
	rep, err := Evaluate(loadTar(t, goodTar(t, `["lib","cordis.patch.yml","*.md","!lib/test.js"]`)), "0.2.0-rc.2")
	if err != nil {
		t.Fatal(err)
	}
	if has(rep, "declared-file-not-in-tarball") {
		t.Fatalf("globs cannot be missing files: %v", rep.Issues)
	}
	if rep.Verdict != OK {
		t.Fatalf("verdict = %q", rep.Verdict)
	}
}
