// Package compat answers one question per plugin: will this bundle load on that
// harness version, and if not, what exactly has to change?
//
// It deliberately checks two different classes of problem together, because a
// user upgrading a profile hits both at once and neither tool reports both:
//
//   - version gating, computed with npm's own range semantics;
//   - bundle structure, where the failure is silent. A package whose patch file
//     is missing from files[] installs cleanly, appears in the profile, and
//     contributes nothing, so the plugin looks broken rather than absent.
package compat

import (
	"fmt"
	"strings"

	"github.com/atop0914/dshfit/internal/manifest"
	"github.com/atop0914/dshfit/internal/semver"
)

// Severity separates "cannot load" from "loads, but the author's intent is
// already broken".
type Severity string

const (
	Block Severity = "block"
	Warn  Severity = "warn"
)

// Verdict is the roll-up shown in the summary line.
type Verdict string

const (
	OK      Verdict = "ok"
	Blocked Verdict = "blocked"
	Warning Verdict = "warn"
)

// Issue is one reason a plugin will not load, or one risk that it will load
// with broken intent.
type Issue struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Package  string   `json:"package,omitempty"`
	Range    string   `json:"range,omitempty"`
	Detail   string   `json:"detail"`
}

// Report is the per-plugin result.
type Report struct {
	Plugin  string  `json:"plugin"`
	Version string  `json:"version"`
	Dir     string  `json:"dir"`
	Host    string  `json:"host"`
	Verdict Verdict `json:"verdict"`
	Issues  []Issue `json:"issues"`
}

// Evaluate runs every check against one host version.
func Evaluate(p *manifest.Plugin, hostStr string) (Report, error) {
	host, err := semver.Parse(hostStr)
	if err != nil {
		return Report{}, fmt.Errorf("host version %q: %w", hostStr, err)
	}
	rep := Report{
		Plugin:  p.Name,
		Version: p.Version,
		Dir:     p.Dir,
		Host:    host.String(),
	}

	rep.Issues = append(rep.Issues, checkBundle(p)...)
	rep.Issues = append(rep.Issues, checkPeers(p, host)...)
	rep.Issues = append(rep.Issues, checkEngines(p, host)...)

	rep.Verdict = OK
	for _, is := range rep.Issues {
		switch is.Severity {
		case Block:
			rep.Verdict = Blocked
		case Warn:
			if rep.Verdict != Blocked {
				rep.Verdict = Warning
			}
		}
	}
	return rep, nil
}

func checkBundle(p *manifest.Plugin) []Issue {
	if p.Bundle == nil || p.Bundle.Patch == "" {
		return []Issue{{
			Severity: Block,
			Code:     "no-bundle",
			Detail: "package.json has no dsh.bundle.patch, so the installer records a dependency " +
				"but appends no bundle to dsh.profile.bundles; the plugin installs and never mounts",
		}}
	}

	var issues []Issue
	patch := p.Bundle.Patch
	if len(p.Files) > 0 && !filesCover(p.Files, patch) {
		issues = append(issues, Issue{
			Severity: Block,
			Code:     "patch-missing-from-files",
			Range:    patch,
			Detail: fmt.Sprintf("files[] does not include %q, so the published tarball omits it while "+
				"dsh.bundle.patch still points at it; the package installs and contributes nothing", patch),
		})
	}

	entries, err := p.BundlePatches()
	if err != nil {
		issues = append(issues, Issue{
			Severity: Block,
			Code:     "patch-unreadable",
			Range:    patch,
			Detail:   err.Error(),
		})
		return issues
	}
	for _, e := range entries {
		if e.Name != "" && e.Name != p.Name {
			issues = append(issues, Issue{
				Severity: Block,
				Code:     "patch-name-mismatch",
				Detail: fmt.Sprintf("patch row %q inserts name %q, but the package is %q; "+
					"cordis cannot resolve the entry", e.ID, e.Name, p.Name),
			})
		}
		if e.ID == "" {
			issues = append(issues, Issue{
				Severity: Warn,
				Code:     "patch-row-without-id",
				Detail:   fmt.Sprintf("patch row for %q has no id, so user config layers cannot target it", e.Name),
			})
		}
	}
	return issues
}

func checkPeers(p *manifest.Plugin, host semver.Version) []Issue {
	var issues []Issue
	for _, name := range p.PeerNames() {
		raw := p.Peers[name]
		rng, err := semver.ParseRange(raw)
		if err != nil {
			issues = append(issues, Issue{
				Severity: Block,
				Code:     "unparseable-peer-range",
				Package:  name,
				Range:    raw,
				Detail:   fmt.Sprintf("peer range %q cannot be parsed: %v", raw, err),
			})
			continue
		}
		exp := rng.Explain(host)
		if exp.Satisfied {
			continue
		}
		issues = append(issues, Issue{
			Severity: Block,
			Code:     "peer-range-excludes-host",
			Package:  name,
			Range:    raw,
			Detail:   exp.Detail,
		})
	}
	return issues
}

// checkEngines reports engines.dsh as a warning, never a block. The field is
// documented as a compatibility declaration that nothing currently enforces, so
// treating it as fatal would reject plugins that load fine today; treating it as
// invisible would hide the exact intent the author wrote down.
func checkEngines(p *manifest.Plugin, host semver.Version) []Issue {
	raw, ok := p.Engines["dsh"]
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	rng, err := semver.ParseRange(raw)
	if err != nil {
		return []Issue{{
			Severity: Warn,
			Code:     "unparseable-engines-dsh",
			Range:    raw,
			Detail:   fmt.Sprintf("engines.dsh %q cannot be parsed: %v", raw, err),
		}}
	}
	if exp := rng.Explain(host); exp.Satisfied {
		return nil
	} else {
		return []Issue{{
			Severity: Warn,
			Code:     "engines-dsh-excludes-host",
			Range:    raw,
			Detail: exp.Detail + "; engines.dsh is declared intent only (no reader enforces it today), " +
				"so this will not stop the install, but the author did not claim support for this host",
		}}
	}
}

// filesCover reports whether an npm files[] entry ships target. An entry covers
// a path if it names it exactly, names a directory holding it, or is a glob that
// starts at a directory holding it.
func filesCover(files []string, target string) bool {
	target = strings.TrimPrefix(filepathSlash(target), "./")
	for _, f := range files {
		f = strings.TrimPrefix(filepathSlash(f), "./")
		if f == target {
			return true
		}
		dir := strings.TrimSuffix(f, "/")
		if dir == "." || dir == "" {
			return true
		}
		if strings.HasSuffix(f, "/**") {
			dir = strings.TrimSuffix(f, "/**")
		}
		if strings.HasPrefix(target, dir+"/") {
			return true
		}
	}
	return false
}

func filepathSlash(s string) string { return strings.ReplaceAll(s, "\\", "/") }

// Count returns how many reports carry each verdict, in one pass.
func Count(reports []Report) (ok, warn, blocked int) {
	for _, r := range reports {
		switch r.Verdict {
		case OK:
			ok++
		case Warning:
			warn++
		case Blocked:
			blocked++
		}
	}
	return ok, warn, blocked
}
