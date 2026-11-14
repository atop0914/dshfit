// Package codes is the one list of finding identifiers this tool can emit.
//
// It exists because an issue code is a promise to whoever reads the output: it
// says "this is the reason, and here is the edit that fixes it". A code that is
// emitted but not documented is a dead end for the reader, and a code that is
// documented but never emitted is a claim with no evidence behind it. Both are
// silent failures, so the list lives in one place that the emitter, the README
// and the tests all have to agree with.
//
// Severity is kept here as a plain string on purpose: `compat.Severity` is the
// type the report carries, and this table describes what that field must be for
// each code. A test compares the two, which is the only way the table can be
// wrong without anything noticing.
package codes

// Severity values, mirroring compat.Block and compat.Warn. A block means the
// package will not load; a warn means it loads but the author's written intent
// is already broken.
const (
	Block = "block"
	Warn  = "warn"
)

// Scope says which input can produce a finding. It matters because three codes
// can only be proven against an archive: a directory has no tarball, so silence
// there is not a pass.
const (
	// Any means both a plugin directory and a tarball can produce the code.
	Any = "any"
	// Tarball means only an archive can: the evidence is what npm actually
	// packed, not what package.json promises.
	Tarball = "tarball"
)

// The identifiers. They are constants rather than literals so a typo is a
// compile error, and so the set that exists is greppable.
const (
	PeerRangeExcludesHost      = "peer-range-excludes-host"
	UnparseablePeerRange       = "unparseable-peer-range"
	NoBundle                   = "no-bundle"
	PatchMissingFromFiles      = "patch-missing-from-files"
	PatchUnreadable            = "patch-unreadable"
	PatchNameMismatch          = "patch-name-mismatch"
	PatchRowWithoutID          = "patch-row-without-id"
	DeclaredFileNotInTarball   = "declared-file-not-in-tarball"
	TarballEntryOutsidePackage = "tarball-entry-outside-package"
	TarballEntryUnsafe         = "tarball-entry-unsafe"
	EnginesDSHExcludesHost     = "engines-dsh-excludes-host"
	UnparseableEnginesDSH      = "unparseable-engines-dsh"
	ManifestUnreadable         = "manifest-unreadable"
)

// Code is one finding identifier with the two facts a reader relies on: how bad
// it is, and whether a directory can produce it.
type Code struct {
	ID       string
	Severity string
	Scope    string
	// Meaning is a short machine-side gloss. The README carries the sentence a
	// user reads; this one exists so the table is not a list of bare strings
	// with no statement of what they mean.
	Meaning string
}

// all is ordered as the README table is: blocks that come from version
// semantics first, then bundle structure, then archive evidence, then the two
// warnings, then the one code that is about the input rather than the package.
var all = []Code{
	{PeerRangeExcludesHost, Block, Any,
		"a @deepseek-ai/dsh* peer range does not admit the host version"},
	{UnparseablePeerRange, Block, Any,
		"the peer range cannot be parsed at all"},
	{NoBundle, Block, Any,
		"no dsh.bundle.patch: installs, never mounts"},
	{PatchMissingFromFiles, Block, Any,
		"files[] omits the patch the bundle points at"},
	{PatchUnreadable, Block, Any,
		"the patch has no readable insert block"},
	{PatchNameMismatch, Block, Any,
		"a patch row inserts a name that is not the package"},
	{DeclaredFileNotInTarball, Block, Tarball,
		"files[] declares a path the archive does not contain"},
	{TarballEntryOutsidePackage, Block, Tarball,
		"an archive entry resolves outside package/"},
	{TarballEntryUnsafe, Block, Tarball,
		"an archive entry is a symlink, hardlink or special file"},
	{EnginesDSHExcludesHost, Warn, Any,
		"the author's declared support window excludes the host"},
	{UnparseableEnginesDSH, Warn, Any,
		"engines.dsh is written but cannot be parsed"},
	{PatchRowWithoutID, Warn, Any,
		"a patch row cannot be targeted by a user config layer"},
	{ManifestUnreadable, Block, Any,
		"the input could not be read as a plugin at all"},
}

// All returns the table. The slice is copied so a caller cannot reorder or
// empty the definition of what this tool reports.
func All() []Code {
	out := make([]Code, len(all))
	copy(out, all)
	return out
}

// Lookup finds one code. The second result is false for an identifier this
// build does not know, which is how a report from another version is
// recognised instead of being treated as an unknown-but-fine code.
func Lookup(id string) (Code, bool) {
	for _, c := range all {
		if c.ID == id {
			return c, true
		}
	}
	return Code{}, false
}

// SeverityOf reports the severity a code must carry, and whether the code is
// known. An unknown code has no severity to check against, and callers should
// treat that as a finding in itself rather than as a default.
func SeverityOf(id string) (string, bool) {
	c, ok := Lookup(id)
	return c.Severity, ok
}

// Blockers and Warnings are the two halves of the table, in table order.
func Blockers() []Code {
	return withSeverity(Block)
}

func Warnings() []Code {
	return withSeverity(Warn)
}

func withSeverity(s string) []Code {
	var out []Code
	for _, c := range all {
		if c.Severity == s {
			out = append(out, c)
		}
	}
	return out
}
