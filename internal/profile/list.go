// Package profile evaluates a whole DSH profile at once: it reads the JSON that
// `dsh plugin --profile <name> list --json` prints, judges every package in it
// against one harness version, and groups the failures by root cause.
//
// Why the package manager's own answer and not a directory walk: the profile is
// owned by pnpm. A walk over node_modules sees hoisted copies, pnpm's content
// addressed store and symlinks, and would report packages that are merely
// transitive. `dsh plugin ... list` is a passthrough to `pnpm list` in the
// profile directory (verified in @deepseek-ai/dsh 0.2.2-rc.1), so it reports
// exactly what the profile declares and what version of it is installed.
package profile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// maxListBytes bounds the JSON we are willing to read. A profile list is a few
// kilobytes; anything orders of magnitude larger is a wrong file, and reading it
// into memory without a cap turns a typo into an OOM.
const maxListBytes = 8 << 20

// Package is one installed dependency as pnpm records it.
//
// Every field is a string and may be empty: pnpm omits `resolved` for linked and
// local packages, and omits `path` when it cannot locate the install. Empty is
// reported as empty; nothing here invents a default.
type Package struct {
	// From is the specifier the profile asked for ("dsh-pet", "npm:other@1.2.3",
	// "link:../plugin"). It is deliberately not parsed: only the installed
	// directory and version are facts this tool can act on.
	From     string `json:"from"`
	Version  string `json:"version"`
	Resolved string `json:"resolved"`
	Path     string `json:"path"`
}

// Project is one workspace member. A DSH profile is a single-package project, so
// this is normally a one-element slice; a pnpm workspace prints one entry per
// member and all of them are judged.
type Project struct {
	Name            string             `json:"name"`
	Version         string             `json:"version"`
	Path            string             `json:"path"`
	Dependencies    map[string]Package `json:"dependencies"`
	DevDependencies map[string]Package `json:"devDependencies"`
}

// Deps merges the two dependency sections into one name-keyed view. A package
// listed in both (pnpm prints each section as declared) is reported once: the
// install is the same one, and reporting it twice would double every count.
func (p Project) Deps() map[string]Package {
	merged := make(map[string]Package, len(p.Dependencies)+len(p.DevDependencies))
	for name, dep := range p.DevDependencies {
		merged[name] = dep
	}
	for name, dep := range p.Dependencies {
		merged[name] = dep
	}
	return merged
}

// Parse reads the JSON printed by `dsh plugin --profile <name> list --json`.
//
// The top-level shape is checked rather than inferred. Both wrong inputs a user
// actually produces — plain `dsh plugin list` text, and a single `package.json` —
// decode into "no projects" if the decoder is merely tolerant, and an empty
// profile is indistinguishable from "nothing to worry about". Refusing with the
// observed shape is the only answer that cannot be mistaken for a clean bill.
func Parse(r io.Reader) ([]Project, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxListBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read profile list: %w", err)
	}
	if len(data) > maxListBytes {
		return nil, fmt.Errorf("profile list is larger than %d MiB, which is not a profile", maxListBytes>>20)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("profile list is empty; `dsh plugin --profile <name> list --json` prints an array")
	}
	switch trimmed[0] {
	case '[':
	case '{':
		return nil, fmt.Errorf("profile list is a JSON object, not an array; " +
			"a single package.json is not a profile listing, and `dsh plugin list` without --json prints text")
	default:
		return nil, fmt.Errorf("profile list starts with %q, not a JSON array; "+
			"run `dsh plugin --profile <name> list --json` and pass its output", trimmed[0])
	}

	var projects []Project
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	if err := dec.Decode(&projects); err != nil {
		return nil, fmt.Errorf("parse profile list: %w", err)
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, fmt.Errorf("parse profile list: more than one JSON value in the input")
	}
	return projects, nil
}
