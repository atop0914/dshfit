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
	"sort"
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
	// Reason carries the semver rejection kind for range issues, so a consumer
	// can tell "the range is too narrow" from "the numbers pass but the
	// prerelease gate refuses". The two need opposite edits; a report that only
	// says "incompatible" makes the reader pick a direction and guess.
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail"`
}

// Report is the per-plugin result.
type Report struct {
	Plugin  string `json:"plugin"`
	Version string `json:"version"`
	// Dir is the path the user pointed at: a plugin directory, or the tarball.
	Dir string `json:"dir"`
	// Source is "dir" or "tarball". It is part of the contract because some
	// findings can only be verified against an archive, and a consumer reading
	// JSON needs to know which ones it is looking at.
	Source  string  `json:"source,omitempty"`
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
		Dir:     p.Location(),
		Source:  string(p.Source),
		Host:    host.String(),
	}

	rep.Issues = append(rep.Issues, checkTarball(p)...)
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

// checkTarball reports what only an archive can prove: that the paths
// package.json declares are actually inside the artifact a user downloads, and
// that nothing in the archive tries to address a path outside the package.
//
// It is silent for directory input on purpose. A checkout has no tarball, so
// "declared but not packed" is not a fact about it, and reporting the absence of
// evidence as a finding would block every directory the user pointed at.
func checkTarball(p *manifest.Plugin) []Issue {
	if p.Source != manifest.TarballSource {
		return nil
	}

	var issues []Issue
	for _, u := range p.Unsafe {
		code := "tarball-entry-outside-package"
		detail := fmt.Sprintf("archive entry %q resolves outside the package root (package/), so "+
			"extracting it could write anywhere on disk; the entry was refused, so the package is "+
			"not safe to install", u.Path)
		if u.Reason != manifest.ReasonOutside {
			code = "tarball-entry-unsafe"
			detail = fmt.Sprintf("archive entry %q is a %s entry; a package that ships links or "+
				"special files can redirect an extractor through them, so the entry was refused",
				u.Path, u.Reason)
		}
		issues = append(issues, Issue{
			Severity: Block,
			Code:     code,
			Range:    u.Path,
			Detail:   detail,
		})
	}

	for _, f := range p.Files {
		if !checkableFileEntry(f) {
			continue
		}
		if entryCovered(p.Entries, f) {
			continue
		}
		issues = append(issues, Issue{
			Severity: Block,
			Code:     "declared-file-not-in-tarball",
			Range:    f,
			Detail: fmt.Sprintf("files[] declares %q but it is not in the archive; npm packed the "+
				"package without it (.npmignore, a skipped prepare, or a stale entry), so whatever "+
				"depends on that path will be missing after install", f),
		})
	}
	return issues
}

// checkableFileEntry reports whether a files[] entry can be answered by looking
// at archive entries at all. Globs and negations cannot: "lib/*.js" is a pattern
// over paths, not a path, and treating its absence as a missing file would block
// packages that are perfectly fine. Refusing to answer is the only honest option,
// and it is invisible only because these forms are a small minority.
func checkableFileEntry(f string) bool {
	f = strings.TrimSpace(f)
	if f == "" || f == "." || f == "./" {
		return false
	}
	if strings.HasPrefix(f, "!") {
		return false // a negation excludes, so it is absent by definition
	}
	return !strings.ContainsAny(f, "*?[]{}")
}

// entryCovered reports whether target names something the archive contains:
// either the entry itself or a directory prefix that holds entries. It does not
// infer that a prefix "should" exist.
func entryCovered(entries []string, target string) bool {
	target = strings.TrimPrefix(filepathSlash(target), "./")
	target = strings.TrimSuffix(target, "/")
	if target == "" {
		return true
	}
	for _, e := range entries {
		if e == target {
			return true
		}
		if strings.HasPrefix(e, target+"/") {
			return true
		}
	}
	return false
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
	// With a real archive the prediction is verifiable, and a stale files[] entry
	// that the tarball disproves must not become a block: the patch demonstrably
	// ships, so the package loads. Without an archive there is nothing to check
	// against, and the declaration is the only evidence available.
	packedAnyway := p.Source == manifest.TarballSource && p.Contains(patch)
	if len(p.Files) > 0 && !filesCover(p.Files, patch) && !packedAnyway {
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

// PeerRejection is one DSH peer range that does not accept a host version.
type PeerRejection struct {
	Package string `json:"package"`
	Range   string `json:"range"`
	// Reason is empty when the range itself cannot be parsed, which is a
	// different problem from a range that parses and refuses the host.
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail"`
}

// RejectingPeers reports every DSH peer range in peers that does not accept
// host, in name order.
//
// This is the only implementation of that judgment, and it is exported on
// purpose: a package checked from disk and the same package checked through the
// registry must reach the same answer about `^0.2.0-rc.1` vs 0.2.2-rc.1. Two
// copies of this logic would drift, and a report that says "your installed copy
// is blocked but the published one is fine" for the same range is not a report
// anyone can act on.
func RejectingPeers(peers map[string]string, host semver.Version) []PeerRejection {
	names := make([]string, 0, len(peers))
	for name := range peers {
		if manifest.IsDSHPackage(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	var out []PeerRejection
	for _, name := range names {
		raw := peers[name]
		rng, err := semver.ParseRange(raw)
		if err != nil {
			out = append(out, PeerRejection{
				Package: name,
				Range:   raw,
				Detail:  fmt.Sprintf("peer range %q cannot be parsed: %v", raw, err),
			})
			continue
		}
		exp := rng.Explain(host)
		if exp.Satisfied {
			continue
		}
		out = append(out, PeerRejection{
			Package: name,
			Range:   raw,
			Reason:  string(exp.Kind),
			Detail:  exp.Detail,
		})
	}
	return out
}

func checkPeers(p *manifest.Plugin, host semver.Version) []Issue {
	var issues []Issue
	for _, r := range RejectingPeers(p.Peers, host) {
		code := "peer-range-excludes-host"
		if r.Reason == "" {
			// A range that does not parse is its own cause: no rewrite of the
			// numbers fixes it, the syntax has to.
			code = "unparseable-peer-range"
		}
		issues = append(issues, Issue{
			Severity: Block,
			Code:     code,
			Package:  r.Package,
			Range:    r.Range,
			Reason:   r.Reason,
			Detail:   r.Detail,
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
	exp := rng.Explain(host)
	if exp.Satisfied {
		return nil
	}
	return []Issue{{
		Severity: Warn,
		Code:     "engines-dsh-excludes-host",
		Range:    raw,
		Reason:   string(exp.Kind),
		Detail: exp.Detail + "; engines.dsh is declared intent only (no reader enforces it today), " +
			"so this will not stop the install, but the author did not claim support for this host",
	}}
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
