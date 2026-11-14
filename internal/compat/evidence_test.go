package compat

import (
	"archive/tar"
	"testing"

	"github.com/atop0914/dshfit/internal/codes"
)

// This file is the evidence half of the README's code table.
//
// A code is a promise to the reader: "this is the reason, here is the edit".
// Three things can quietly break that promise — a code that is documented and
// never emitted, a code that is emitted with a different severity than the
// table says, and a code that can only come from an archive appearing against a
// directory. (A fourth, a code that is emitted and not documented, is caught by
// internal/docs against the same table.)
//
// The table below is keyed by the identifiers in package codes and the test
// iterates that package, not this slice: adding a code to the registry without
// a fixture here fails the test, and adding a fixture for a code the registry
// does not know fails too.

type positiveCase struct {
	code string
	// what names the shape a user would hit, so a failure reads as a scenario
	// rather than as a broken fixture.
	what  string
	build func(t *testing.T) Report
}

func evalDir(t *testing.T, host, pkg string, extra map[string]string) Report {
	t.Helper()
	rep, err := Evaluate(load(t, writePlugin(t, pkg, extra)), host)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return rep
}

// pkgWithPatch renders a plugin that is clean apart from what the caller varies.
func pkgWithPatch(name, files, peers, engines string) string {
	s := `{"name":"` + name + `","version":"1.0.0","files":` + files
	if peers != "" {
		s += `,"peerDependencies":` + peers
	}
	if engines != "" {
		s += `,"engines":` + engines
	}
	s += `,"dsh":{"bundle":{"patch":"./cordis.patch.yml"}}}`
	return s
}

func rowWith(id, name string) string {
	s := "- insert:\n    - "
	if id != "" {
		s += "id: " + id + "\n      "
	}
	return s + "name: " + name + "\n"
}

func positiveCases() []positiveCase {
	const clean = `^0.2.0-rc.2`
	return []positiveCase{
		{
			code: codes.PeerRangeExcludesHost,
			what: "a 0.1.x caret against a 0.2 host",
			build: func(t *testing.T) Report {
				return evalDir(t, "0.2.0-rc.2",
					pkgWithPatch("dsh-plugin-p", `["cordis.patch.yml"]`,
						`{"@deepseek-ai/dsh-app-boot":"^0.1.0-rc.6"}`, ""),
					map[string]string{"cordis.patch.yml": rowWith("dsh-plugin-p", "dsh-plugin-p")})
			},
		},
		{
			code: codes.UnparseablePeerRange,
			what: "a peer range that is not a range",
			build: func(t *testing.T) Report {
				return evalDir(t, "0.2.0-rc.2",
					pkgWithPatch("dsh-plugin-p", `["cordis.patch.yml"]`,
						`{"@deepseek-ai/dsh-app-boot":"^^1.2.3"}`, ""),
					map[string]string{"cordis.patch.yml": rowWith("dsh-plugin-p", "dsh-plugin-p")})
			},
		},
		{
			code: codes.NoBundle,
			what: "package.json without dsh.bundle.patch",
			build: func(t *testing.T) Report {
				return evalDir(t, "0.2.0-rc.2",
					`{"name":"dsh-plugin-nb","version":"1.0.0"}`,
					map[string]string{"cordis.patch.yml": rowWith("dsh-plugin-nb", "dsh-plugin-nb")})
			},
		},
		{
			code: codes.PatchMissingFromFiles,
			what: "files[] omits the patch the bundle points at",
			build: func(t *testing.T) Report {
				return evalDir(t, "0.2.0-rc.2",
					pkgWithPatch("dsh-plugin-pm", `["lib"]`, `{"@deepseek-ai/dsh-app-boot":"`+clean+`"}`, ""),
					map[string]string{
						"cordis.patch.yml": rowWith("dsh-plugin-pm", "dsh-plugin-pm"),
						"lib/index.js":     "export default 1\n",
					})
			},
		},
		{
			code: codes.PatchUnreadable,
			what: "a patch whose insert block is a scalar row",
			build: func(t *testing.T) Report {
				return evalDir(t, "0.2.0-rc.2",
					pkgWithPatch("dsh-plugin-pu", `["cordis.patch.yml"]`,
						`{"@deepseek-ai/dsh-app-boot":"`+clean+`"}`, ""),
					map[string]string{"cordis.patch.yml": "- insert:\n    scalar-row-without-a-dash\n"})
			},
		},
		{
			code: codes.PatchNameMismatch,
			what: "a patch row inserting another package's name",
			build: func(t *testing.T) Report {
				return evalDir(t, "0.2.0-rc.2",
					pkgWithPatch("dsh-plugin-nm", `["cordis.patch.yml"]`,
						`{"@deepseek-ai/dsh-app-boot":"`+clean+`"}`, ""),
					map[string]string{"cordis.patch.yml": rowWith("row-1", "dsh-something-else")})
			},
		},
		{
			code: codes.PatchRowWithoutID,
			what: "an insert row with a name and no id",
			build: func(t *testing.T) Report {
				return evalDir(t, "0.2.0-rc.2",
					pkgWithPatch("dsh-plugin-ni", `["cordis.patch.yml"]`,
						`{"@deepseek-ai/dsh-app-boot":"`+clean+`"}`, ""),
					map[string]string{"cordis.patch.yml": rowWith("", "dsh-plugin-ni")})
			},
		},
		{
			code: codes.EnginesDSHExcludesHost,
			what: "engines.dsh narrower than the peer range",
			build: func(t *testing.T) Report {
				return evalDir(t, "0.2.0-rc.2",
					pkgWithPatch("dsh-plugin-en", `["cordis.patch.yml"]`,
						`{"@deepseek-ai/dsh-app-boot":"`+clean+`"}`, `{"dsh":"^0.1.0"}`),
					map[string]string{"cordis.patch.yml": rowWith("dsh-plugin-en", "dsh-plugin-en")})
			},
		},
		{
			code: codes.UnparseableEnginesDSH,
			what: "engines.dsh that cannot be parsed",
			build: func(t *testing.T) Report {
				return evalDir(t, "0.2.0-rc.2",
					pkgWithPatch("dsh-plugin-ue", `["cordis.patch.yml"]`,
						`{"@deepseek-ai/dsh-app-boot":"`+clean+`"}`, `{"dsh":"banana"}`),
					map[string]string{"cordis.patch.yml": rowWith("dsh-plugin-ue", "dsh-plugin-ue")})
			},
		},
		{
			code: codes.DeclaredFileNotInTarball,
			what: "files[] declares a path npm did not pack",
			build: func(t *testing.T) Report {
				tar := makeTarball(t,
					archive{name: "package/", typ: tar.TypeDir},
					archive{name: "package/package.json", body: tarPkgWith(`["lib","docs","cordis.patch.yml"]`)},
					archive{name: "package/cordis.patch.yml", body: tarPatch},
					archive{name: "package/lib/index.js", body: "export default 1\n"},
				)
				rep, err := Evaluate(loadTar(t, tar), "0.2.0-rc.2")
				if err != nil {
					t.Fatalf("Evaluate: %v", err)
				}
				return rep
			},
		},
		{
			code: codes.TarballEntryOutsidePackage,
			what: "an archive entry that escapes package/",
			build: func(t *testing.T) Report {
				tar := makeTarball(t,
					archive{name: "package/", typ: tar.TypeDir},
					archive{name: "package/package.json", body: tarPkgWith(`["lib","cordis.patch.yml"]`)},
					archive{name: "package/cordis.patch.yml", body: tarPatch},
					archive{name: "../etc/passwd", body: "root:x:0:0\n"},
				)
				rep, err := Evaluate(loadTar(t, tar), "0.2.0-rc.2")
				if err != nil {
					t.Fatalf("Evaluate: %v", err)
				}
				return rep
			},
		},
		{
			code: codes.TarballEntryUnsafe,
			what: "a symlink entry inside the archive",
			build: func(t *testing.T) Report {
				tar := makeTarball(t,
					archive{name: "package/", typ: tar.TypeDir},
					archive{name: "package/package.json", body: tarPkgWith(`["lib","cordis.patch.yml"]`)},
					archive{name: "package/cordis.patch.yml", body: tarPatch},
					archive{name: "package/link", body: "/etc/passwd", typ: tar.TypeSymlink},
				)
				rep, err := Evaluate(loadTar(t, tar), "0.2.0-rc.2")
				if err != nil {
					t.Fatalf("Evaluate: %v", err)
				}
				return rep
			},
		},
	}
}

// exemptFromCompatFixture is the only code that cannot be produced here, and
// the reason has to be a property of where it comes from: manifest-unreadable is
// emitted by the CLI when manifest.LoadPath fails, which means there is no
// *manifest.Plugin* to Evaluate. Its positive case lives in the cli package
// (TestUnreadablePluginDoesNotStopTheRun); listing it here keeps the exemption
// visible instead of letting the coverage loop quietly skip it.
var exemptFromCompatFixture = map[string]string{
	codes.ManifestUnreadable: "emitted by internal/cli before a plugin exists; see TestUnreadablePluginDoesNotStopTheRun",
}

func TestEveryDocumentedCodeHasAPositiveCase(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range positiveCases() {
		if _, ok := codes.Lookup(c.code); !ok {
			t.Errorf("fixture for %q: that code is not in the registry", c.code)
			continue
		}
		if seen[c.code] {
			t.Errorf("%q has more than one fixture; the second one is never the evidence", c.code)
			continue
		}
		seen[c.code] = true

		rep := c.build(t)
		if !has(rep, c.code) {
			t.Errorf("%s (%s): fixture produced %v, which does not contain it",
				c.code, c.what, issueCodes(rep))
			continue
		}
		// Severity is part of the promise: a block that is emitted as a warning
		// makes a plugin that will not load look like advice.
		want, _ := codes.SeverityOf(c.code)
		for _, is := range rep.Issues {
			got, known := codes.SeverityOf(is.Code)
			if !known {
				t.Errorf("%s (%s): emitted unknown code %q", c.code, c.what, is.Code)
				continue
			}
			if got != string(is.Severity) {
				t.Errorf("%s (%s): issue %q has severity %q, table says %q",
					c.code, c.what, is.Code, is.Severity, got)
			}
		}
		if want == "" {
			t.Errorf("%s: registry has no severity", c.code)
		}
	}

	for _, c := range codes.All() {
		if _, exempt := exemptFromCompatFixture[c.ID]; exempt {
			continue
		}
		if !seen[c.ID] {
			t.Errorf("code %q has no positive case: a documented code with no fixture is a claim "+
				"with no evidence behind it", c.ID)
		}
	}
	for id, why := range exemptFromCompatFixture {
		if _, ok := codes.Lookup(id); !ok {
			t.Errorf("exemption for unknown code %q (%s)", id, why)
		}
		if _, ok := seen[id]; ok {
			t.Errorf("%q has a fixture but is still listed as exempt", id)
		}
	}
}

// TestDirectoryModeNeverEmitsArchiveCodes is the reverse assertion behind the
// README's "Directory input never produces these codes": a checkout has no
// archive, so "declared but not packed" is not a fact about it, and reporting
// the absence of evidence would block every directory a user pointed at.
func TestDirectoryModeNeverEmitsArchiveCodes(t *testing.T) {
	for _, c := range positiveCases() {
		rep := c.build(t)
		if rep.Source == "tarball" {
			continue
		}
		for _, is := range rep.Issues {
			info, ok := codes.Lookup(is.Code)
			if !ok {
				continue
			}
			if info.Scope == codes.Tarball {
				t.Errorf("%s: directory input produced archive-only code %q", c.what, is.Code)
			}
		}
	}
}

// TestArchiveCasesReallyAreArchives keeps the reverse assertion above from
// passing vacuously: if a tarball fixture silently fell back to directory mode,
// the loop would skip it and report nothing.
func TestArchiveCasesReallyAreArchives(t *testing.T) {
	archiveCodes := map[string]bool{
		codes.DeclaredFileNotInTarball:   true,
		codes.TarballEntryOutsidePackage: true,
		codes.TarballEntryUnsafe:         true,
	}
	checked := 0
	for _, c := range positiveCases() {
		if !archiveCodes[c.code] {
			continue
		}
		checked++
		if rep := c.build(t); rep.Source != "tarball" {
			t.Errorf("%s: source = %q, want tarball", c.code, rep.Source)
		}
	}
	if checked != len(archiveCodes) {
		t.Fatalf("covered %d archive codes, registry scope says %d", checked, len(archiveCodes))
	}
}
