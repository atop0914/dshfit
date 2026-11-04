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

// Plugin is the subset of package.json that decides loadability.
type Plugin struct {
	Dir     string
	Name    string
	Version string
	Peers   map[string]string
	Engines map[string]string
	Files   []string
	Bundle  *Bundle
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
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if pkg.Name == "" {
		return nil, fmt.Errorf("parse %s: missing name", path)
	}
	p := &Plugin{
		Dir:     dir,
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
	path := filepath.Join(p.Dir, filepath.FromSlash(p.Bundle.Patch))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	entries, err := parseInsertEntries(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return entries, nil
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
