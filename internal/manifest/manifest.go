// Package manifest reads what a DSH plugin bundle actually promises on disk.
//
// Three of the installer's failure modes are structural and visible before any
// code runs: a package that never declares dsh.bundle, a bundle whose patch file
// is not in package.json files[] (so it installs and never mounts), and a patch
// whose inserted name does not match the package name.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Source records where a Plugin was read from. The distinction is load-bearing:
// some findings can only be verified against a real tarball, so reporting them
// for a directory checkout would be a claim with no evidence behind it.
type Source string

const (
	// DirSource is an unpacked plugin directory, possibly with node_modules.
	DirSource Source = "dir"
	// TarballSource is the .tgz npm publishes: the archive is ground truth for
	// what a user actually downloads and installs.
	TarballSource Source = "tarball"
)

// Plugin is the subset of package.json that decides loadability.
type Plugin struct {
	Dir string
	// Tarball is the archive path when Source is TarballSource.
	Tarball string
	Source  Source
	// Entries lists the package-relative slash paths the archive actually
	// contains, sorted. Nil for directory input, where the filesystem answers
	// that question instead.
	Entries []string
	// Unsafe lists archive entries refused while reading: paths that escape the
	// package root, and links or special files. They are kept for reporting but
	// never parsed, so a hostile archive cannot influence the verdict silently.
	Unsafe []UnsafeEntry

	Name    string
	Version string
	Peers   map[string]string
	Engines map[string]string
	Files   []string
	Bundle  *Bundle

	// mem holds the archive contents. Non-nil only for tarball input, and it is
	// what keeps LoadTarball from writing anything to disk.
	mem map[string][]byte
}

// UnsafeEntry is one archive entry the reader refused.
type UnsafeEntry struct {
	Path   string
	Reason string
}

// Reasons an entry is refused.
const (
	// ReasonOutside marks a path that resolves outside the package root.
	ReasonOutside = "outside-package"
	// ReasonLink marks a symlink or hardlink, which an extractor can be tricked
	// into following.
	ReasonLink = "link"
	// ReasonSpecial marks a device, fifo, or other non-regular entry.
	ReasonSpecial = "special-file"
)

// Location is the path the user typed: the directory or the tarball.
func (p *Plugin) Location() string {
	if p.Tarball != "" {
		return p.Tarball
	}
	return p.Dir
}

// Bundle points at the config layer this package contributes.
type Bundle struct {
	Patch string
}

type packageJSON struct {
	Name             string            `json:"name"`
	Version          string            `json:"version"`
	PeerDependencies map[string]string `json:"peerDependencies"`
	Engines          map[string]string `json:"engines"`
	Files            []string          `json:"files"`
	DSH              struct {
		Bundle *struct {
			Patch string `json:"patch"`
		} `json:"bundle"`
	} `json:"dsh"`
}

// Load reads dir/package.json. It tolerates unknown fields, because a checker
// that rejects manifests for containing extra keys would flag every plugin in
// the ecosystem.
func Load(dir string) (*Plugin, error) {
	path := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	p, err := parsePlugin(data, path)
	if err != nil {
		return nil, err
	}
	p.Dir = dir
	p.Source = DirSource
	return p, nil
}

// LoadPath reads whatever the user pointed at: a plugin directory or an npm
// tarball. The content sniff comes before the extension because a tarball saved
// under a different name is still a tarball, while a file that merely ends in
// .tgz is not one and will be rejected with the reader's own error rather than a
// guess about what was meant.
func LoadPath(p string) (*Plugin, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	if info.IsDir() {
		return Load(p)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("read %s: neither a directory nor a regular file", p)
	}
	gzipped, err := hasGzipMagic(p)
	if err != nil {
		return nil, err
	}
	if gzipped || isTarballName(p) {
		return LoadTarball(p)
	}
	return nil, fmt.Errorf("read %s: not a plugin directory and not a gzip tarball "+
		"(no 1f 8b magic, and the name does not end in .tgz/.tar.gz)", p)
}

func isTarballName(p string) bool {
	lower := strings.ToLower(p)
	return strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz")
}

// parsePlugin builds a Plugin from package.json bytes, wherever they came from.
// where names the origin in error messages, so a failure inside an archive still
// points at something the user can open.
func parsePlugin(data []byte, where string) (*Plugin, error) {
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", where, err)
	}
	if pkg.Name == "" {
		return nil, fmt.Errorf("parse %s: missing name", where)
	}
	p := &Plugin{
		Name:    pkg.Name,
		Version: pkg.Version,
		Peers:   pkg.PeerDependencies,
		Engines: pkg.Engines,
		Files:   pkg.Files,
	}
	if pkg.DSH.Bundle != nil {
		p.Bundle = &Bundle{Patch: pkg.DSH.Bundle.Patch}
	}
	return p, nil
}

// PeerNames returns the DSH packages this plugin declares a peer range for,
// sorted so output is stable.
func (p *Plugin) PeerNames() []string {
	names := make([]string, 0, len(p.Peers))
	for name := range p.Peers {
		if IsDSHPackage(name) {
			names = append(names, name)
		}
	}
	sortStrings(names)
	return names
}

// IsDSHPackage reports whether name is the harness itself or one of its
// packages. The 0.2 line renamed several of these, so matching the prefix rather
// than a fixed list keeps the checker working across releases.
func IsDSHPackage(name string) bool {
	return name == "@deepseek-ai/dsh" || strings.HasPrefix(name, "@deepseek-ai/dsh-")
}

// BundlePatches returns the id/name pairs inserted by the bundle's patch file.
//
// This is a targeted line scanner, not a YAML parser, and the distinction is
// deliberate: pulling in a YAML library to read two keys would break the
// zero-dependency gate, and a full parser would still not know what a "correct"
// DSH config layer looks like. What it can do is refuse to guess. Anything it
// cannot read is reported as unreadable rather than skipped.
func (p *Plugin) BundlePatches() ([]PatchEntry, error) {
	if p.Bundle == nil || p.Bundle.Patch == "" {
		return nil, fmt.Errorf("%s declares no dsh.bundle.patch", p.Name)
	}
	rel := packPath(p.Bundle.Patch)
	data, err := p.read(rel)
	if err != nil {
		return nil, err
	}
	entries, err := parseInsertEntries(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	return entries, nil
}

// read returns a package-relative file, from the archive contents or from the
// directory. Both paths error out when the file is absent: a missing patch is
// the difference between "the plugin contributes nothing" and "the plugin is
// fine", and returning empty bytes would erase that difference.
func (p *Plugin) read(rel string) ([]byte, error) {
	if p.mem != nil {
		body, ok := p.mem[rel]
		if !ok {
			return nil, fmt.Errorf("%s is not in the tarball %s", rel, p.Tarball)
		}
		return body, nil
	}
	full := filepath.Join(p.Dir, filepath.FromSlash(rel))
	body, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", full, err)
	}
	return body, nil
}

// Contains reports whether the plugin actually ships rel. For a directory that
// means it exists on disk; for a tarball it means an entry exists, which is the
// only question that distinguishes "declared" from "delivered".
func (p *Plugin) Contains(rel string) bool {
	rel = packPath(rel)
	if p.mem != nil {
		_, ok := p.mem[rel]
		return ok
	}
	info, err := os.Stat(filepath.Join(p.Dir, filepath.FromSlash(rel)))
	return err == nil && !info.IsDir()
}

// packPath normalises a path from package.json to the slash form used both by
// tar entries and by the archive's in-memory key space.
func packPath(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	for strings.HasPrefix(s, "./") {
		s = s[2:]
	}
	return s
}

// PatchEntry is one row the patch inserts into the config tree.
type PatchEntry struct {
	ID   string
	Name string
}

func parseInsertEntries(src string) ([]PatchEntry, error) {
	var (
		entries  []PatchEntry
		inInsert bool
		cur      *PatchEntry
		base     = -1
	)
	for _, line := range strings.Split(src, "\n") {
		body := stripComment(line)
		trimmed := strings.TrimSpace(body)
		if trimmed == "" {
			continue
		}
		indent := len(body) - len(strings.TrimLeft(body, " "))
		if !inInsert {
			if strings.TrimPrefix(trimmed, "- ") == "insert:" || trimmed == "insert:" {
				inInsert = true
				base = indent
			}
			continue
		}
		if indent <= base {
			break // left the insert block
		}
		if strings.HasPrefix(trimmed, "- ") {
			if cur != nil {
				entries = append(entries, *cur)
			}
			cur = &PatchEntry{}
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
		}
		if cur == nil {
			return nil, fmt.Errorf("insert block has a scalar row %q", trimmed)
		}
		key, val, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "id":
			cur.ID = unquote(val)
		case "name":
			cur.Name = unquote(val)
		}
	}
	if cur != nil {
		entries = append(entries, *cur)
	}
	if !inInsert {
		return nil, fmt.Errorf("no insert block")
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("insert block is empty")
	}
	return entries, nil
}

func stripComment(line string) string {
	// A '#' inside a quoted value is data, not a comment. DSH patch values are
	// package ids and names, so a quote check is sufficient here.
	inQuote := false
	for i, r := range line {
		switch r {
		case '"', '\'':
			inQuote = !inQuote
		case '#':
			if !inQuote {
				return line[:i]
			}
		}
	}
	return line
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
