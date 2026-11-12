package profile

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/atop0914/dshfit/internal/compat"
	"github.com/atop0914/dshfit/internal/fix"
	"github.com/atop0914/dshfit/internal/manifest"
	"github.com/atop0914/dshfit/internal/semver"
)

// Status is what the tool was able to say about one package. It is explicit for
// every package in the listing, because the one thing a profile report must not
// do is leave a package out and let the reader assume it was fine.
type Status string

const (
	// StatusPlugin is a package that declares a bundle and was judged.
	StatusPlugin Status = "plugin"
	// StatusNotAPlugin is a readable package with no dsh.bundle.patch. It is not
	// a plugin, so "will it load" is not a question about it. In a profile this
	// is the normal case for data and tooling packages, which is why it is not a
	// finding — unlike a directory the user pointed at directly, where the same
	// shape means the user picked something that will never mount.
	StatusNotAPlugin Status = "not-a-plugin"
	// StatusUnknown is a package that had to be judged and could not be: no
	// directory recorded, no readable package.json, or an ambiguous duplicate.
	// It is never silently folded into "ok".
	StatusUnknown Status = "unknown"
)

// Entry is one package in the profile.
type Entry struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Dir     string `json:"dir,omitempty"`
	Status  Status `json:"status"`
	// Reason explains StatusUnknown and StatusNotAPlugin. For a judged plugin it
	// is empty: the verdict and issues carry the explanation.
	Reason string         `json:"reason,omitempty"`
	Report *compat.Report `json:"report,omitempty"`
	// Registry is filled only by AnnotateRegistry, only for blocked plugins,
	// and only when the caller asked for it. It is a second question ("has the
	// publisher shipped a release that works?") and never changes Status,
	// Verdict or Counts.
	Registry *RegistryAnswer `json:"registry,omitempty"`
}

// Group is one root cause, with every plugin it affects. Grouping is the point
// of profile mode: twenty caret-0.x plugins are one mistake made twenty times,
// and a per-plugin listing hides exactly that.
type Group struct {
	Severity compat.Severity `json:"severity"`
	Code     string          `json:"code"`
	// Reason separates two diagnoses that share a code. `peer-range-excludes-host`
	// with reason `out-of-range` needs a wider range; the same code with reason
	// `prerelease-gate` needs the range to name the new core explicitly. Merging
	// them would hand out the wrong fix, so they are different groups.
	Reason string `json:"reason,omitempty"`
	// Range is the peer range or patch path the issues share.
	Range string `json:"range,omitempty"`
	// Targets are the packages the issues name, e.g. the peers that reject the
	// host. Empty when the issue is about the plugin itself.
	Targets []string `json:"targets,omitempty"`
	// Plugins are the plugin names this root cause hits, sorted.
	Plugins []string `json:"plugins"`
	// Fix is the smallest change that addresses the cause. It is a candidate, not
	// a proof: re-running is what confirms it.
	Fix string `json:"fix"`
	// FixCandidates are rewrites of Range that the range engine itself accepted.
	// Fix says which direction to move; these say exactly what to write, and each
	// one was re-parsed and re-tested against the host before it was kept. Empty
	// for causes that are not about version ranges.
	FixCandidates []fix.Candidate `json:"fix_candidates,omitempty"`
	// FixTried records the shapes the solver generated and the engine refused,
	// with the engine's own reason. It is normally empty; for a prerelease host
	// it holds the tagless rewrite (`|| >=0.2.2`), which is the shape authors
	// actually write and the one that does not work.
	FixTried []fix.Attempt `json:"fix_tried,omitempty"`
	// FixRefusal explains why a range cause has no candidate: the range does not
	// parse, or it already admits the host. Empty when candidates exist, and
	// empty for non-range causes, whose Fix text is already the whole answer.
	FixRefusal string `json:"fix_refusal,omitempty"`
}

// Counts is the one-line summary. Plugins counts judged plugins only, so
// Plugins == OK + Warn + Blocked always holds.
type Counts struct {
	Packages   int `json:"packages"`
	Plugins    int `json:"plugins"`
	OK         int `json:"ok"`
	Warn       int `json:"warn"`
	Blocked    int `json:"blocked"`
	NotAPlugin int `json:"not_a_plugin"`
	Unknown    int `json:"unknown"`
}

// Result is the whole answer for one host version.
type Result struct {
	Host string `json:"host"`
	// Projects are the workspace members the listing contained.
	Projects []string `json:"projects"`
	// Entries covers every package in the listing, sorted by name.
	Entries []Entry `json:"entries"`
	// Groups covers judged plugins only, blocked causes first.
	Groups []Group `json:"groups"`
	Counts Counts  `json:"counts"`
}

// Assess judges every package in the listing against one host version.
//
// base is the directory the listing was read from, and it exists because a
// relative path in a listing is relative to that file, not to wherever the
// process happens to run. pnpm prints absolute paths, so base only changes the
// answer for a hand-written listing — which is exactly the case where guessing
// the wrong root would silently judge the wrong directories.
//
// An error means the question could not be asked at all (an unparseable host
// version). Everything wrong with an individual package becomes an entry with an
// explicit status, because aborting on the first unreadable checkout would hide
// the other nineteen verdicts the user came for.
func Assess(projects []Project, hostStr, base string) (Result, error) {
	host, err := semver.Parse(hostStr)
	if err != nil {
		return Result{}, fmt.Errorf("host version %q: %w", hostStr, err)
	}
	res := Result{Host: host.String(), Entries: []Entry{}, Groups: []Group{}}

	seen := make(map[string]int, 8)
	for _, proj := range projects {
		res.Projects = append(res.Projects, projectLabel(proj))
		deps := proj.Deps()
		for _, name := range sortedNames(deps) {
			dep := deps[name]
			dir := resolveDir(proj, dep.Path, base)
			if idx, dup := seen[name]; dup {
				prev := res.Entries[idx]
				if prev.Dir == dir {
					continue // the same install, listed in both dependency sections
				}
				res.Entries[idx] = Entry{
					Name:    name,
					Version: dep.Version,
					Dir:     dir,
					Status:  StatusUnknown,
					Reason: fmt.Sprintf("the same package name resolves to two directories (%s and %s); "+
						"which copy the host loads cannot be determined from the listing", prev.Dir, dir),
				}
				continue
			}
			seen[name] = len(res.Entries)
			res.Entries = append(res.Entries, judge(name, dep, dir, host))
		}
	}

	sort.SliceStable(res.Entries, func(i, j int) bool { return res.Entries[i].Name < res.Entries[j].Name })
	res.Counts = countEntries(res.Entries)
	res.Groups = groupEntries(res.Entries, res.Host)
	return res, nil
}

func judge(name string, dep Package, dir string, host semver.Version) Entry {
	e := Entry{Name: name, Version: dep.Version, Dir: dir}
	if dir == "" {
		e.Status = StatusUnknown
		e.Reason = "the listing records no directory for this package, so there is no manifest to read; " +
			"a linked or local install has to be pointed at directly"
		return e
	}
	p, err := manifest.Load(dir)
	if err != nil {
		e.Status = StatusUnknown
		e.Reason = "manifest unreadable: " + err.Error()
		return e
	}
	if e.Version == "" {
		e.Version = p.Version
	}
	if p.Bundle == nil || p.Bundle.Patch == "" {
		e.Status = StatusNotAPlugin
		e.Reason = "no dsh.bundle.patch, so this dependency is not a plugin and the installer mounts nothing for it"
		return e
	}
	rep, err := compat.Evaluate(p, host.String())
	if err != nil {
		e.Status = StatusUnknown
		e.Reason = "cannot evaluate: " + err.Error()
		return e
	}
	e.Status = StatusPlugin
	e.Report = &rep
	return e
}

// resolveDir turns the directory the listing recorded into a path this process
// can open, resolving relative parts against the project directory and then
// against the listing's own directory.
func resolveDir(proj Project, depPath, base string) string {
	if depPath == "" {
		return ""
	}
	if filepath.IsAbs(depPath) {
		return filepath.Clean(depPath)
	}
	root := proj.Path
	if root == "" {
		root = base
	} else if !filepath.IsAbs(root) && base != "" {
		root = filepath.Join(base, root)
	}
	if root == "" {
		return filepath.Clean(depPath)
	}
	return filepath.Clean(filepath.Join(root, depPath))
}

func projectLabel(p Project) string {
	if p.Name != "" {
		return p.Name
	}
	if p.Path != "" {
		return filepath.ToSlash(p.Path)
	}
	return "(unnamed project)"
}

func sortedNames(deps map[string]Package) []string {
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func countEntries(entries []Entry) Counts {
	c := Counts{Packages: len(entries)}
	for _, e := range entries {
		switch e.Status {
		case StatusNotAPlugin:
			c.NotAPlugin++
			continue
		case StatusUnknown:
			c.Unknown++
			continue
		}
		c.Plugins++
		switch {
		case e.Report == nil:
			// A judged entry without a report would be a bug, not a verdict;
			// counting it as ok is the one thing that must not happen.
			c.Unknown++
		case e.Report.Verdict == compat.Blocked:
			c.Blocked++
		case e.Report.Verdict == compat.Warning:
			c.Warn++
		default:
			c.OK++
		}
	}
	return c
}

// groupEntries folds the issues of judged plugins into one row per root cause.
//
// The key is code + range, deliberately *not* the package name. The tie was
// measured on real data: dsh-pet@0.4.5 rejects the 0.2.2 line through nine
// different peers that all declare `^0.2.0-rc.1`, so a per-package key prints
// nine groups of one plugin each for a single mistake. Packages stay in the
// group as targets, which is where a reader wants them.
//
// Reason is not in the key either, and that is not an oversight: within one run
// it is a function of (code, range, host) — the same range judged against the
// same host is rejected for the same reason — so keying on it would be dead
// weight. It is carried in the group because it decides the prescription, and
// keeping the two rejection kinds apart is tested through that instead of
// through a key component that can never differ on its own.
//
// Unreadable and non-plugin entries never reach a group: they have no range, so
// calling them a compatibility cause would be an attribution with no evidence.
func groupEntries(entries []Entry, host string) []Group {
	type bucket struct {
		group   Group
		plugins map[string]bool
		targets map[string]bool
	}
	var (
		order   []string
		buckets = make(map[string]*bucket, 4)
	)
	for _, e := range entries {
		if e.Status != StatusPlugin || e.Report == nil {
			continue
		}
		for _, is := range e.Report.Issues {
			key := is.Code + "\x00" + is.Range
			b, ok := buckets[key]
			if !ok {
				b = &bucket{
					group:   Group{Severity: is.Severity, Code: is.Code, Reason: is.Reason, Range: is.Range},
					plugins: map[string]bool{},
					targets: map[string]bool{},
				}
				buckets[key] = b
				order = append(order, key)
			}
			// A block anywhere in the bucket decides the bucket: a group headed
			// "warn" that actually contains a blocker would be read as optional.
			if is.Severity == compat.Block {
				b.group.Severity = compat.Block
			}
			b.plugins[e.Name] = true
			if is.Package != "" {
				b.targets[is.Package] = true
			}
		}
	}

	groups := make([]Group, 0, len(order))
	for _, key := range order {
		b := buckets[key]
		b.group.Plugins = sortedSet(b.plugins)
		b.group.Targets = sortedSet(b.targets)
		b.group.Fix = prescription(b.group, host)
		attachFix(&b.group, host)
		groups = append(groups, b.group)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if a.Severity != b.Severity {
			return a.Severity == compat.Block
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.Range != b.Range {
			return a.Range < b.Range
		}
		return a.Reason < b.Reason
	})
	return groups
}

func sortedSet(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// prescription names the smallest change that addresses a cause.
//
// It is worded as a candidate because that is what it is: this tool does not
// look up what the registry publishes, so it cannot promise a particular range
// exists. What it can promise is that the two rejection kinds need opposite
// edits, which is why they are never merged into "incompatible".
func prescription(g Group, host string) string {
	switch g.Code {
	case "peer-range-excludes-host":
		if g.Reason == string(semver.KindPreReleaseGate) {
			return fmt.Sprintf("the numbers already pass; only npm's prerelease gate refuses %s. "+
				"Add a clause naming that exact core with a prerelease tag — `|| >=%s` — which is the "+
				"shape dsh-context uses for the 0.2.2 line. Re-run to confirm.", host, host)
		}
		return fmt.Sprintf("the declared range stops before %s: widen the lower or upper bound "+
			"(appending `|| >=%s` is the smallest widening). Re-run to confirm.", host, host)
	case "unparseable-peer-range":
		return "the range cannot be parsed, so npm cannot admit any version through it; fix the syntax first"
	case "engines-dsh-excludes-host":
		return fmt.Sprintf("declare support for %s in engines.dsh if it is real. Nothing enforces this "+
			"field today, so it changes no verdict — it is the author's stated window, and it is currently "+
			"narrower than the range above.", host)
	case "patch-missing-from-files":
		return fmt.Sprintf("add %q to files[] (or stop excluding it): the published tarball ships no patch, "+
			"so the plugin installs and mounts nothing", g.Range)
	case "patch-unreadable":
		return fmt.Sprintf("%q must exist and contain an insert block; without a readable patch the "+
			"plugin mounts nothing", g.Range)
	case "patch-name-mismatch":
		return "the patch must insert an entry whose name equals the package name, or cordis cannot resolve it"
	case "patch-row-without-id":
		return "give the inserted row an id so user config layers can target it"
	case "unparseable-engines-dsh":
		return "engines.dsh cannot be parsed, so the stated support window is unreadable; fix the syntax"
	}
	return "no automatic prescription for this code; read the detail"
}
