# dshfit

**Know which DeepSeek Harness plugins will stop loading — before you upgrade.**

```console
$ dshfit check --host 0.2.0-rc.2 ~/.dsh/profiles/web/plugins/*/
BLOCKED  dsh-openviking@2.0.1  /home/me/.dsh/profiles/web/plugins/dsh-openviking
  block peer-range-excludes-host  @deepseek-ai/dsh-app-boot ^0.1.0-rc.6
        0.2.0-rc.2 is a prerelease of 0.2.0, and no comparator in ">=0.1.0-rc.6 <0.2.0"
        names 0.2.0 with a prerelease tag; npm only admits prereleases that the
        range explicitly opts into
OK       dsh-cost-meter@1.4.0  /home/me/.dsh/profiles/web/plugins/dsh-cost-meter

2 plugin(s) against host 0.2.0-rc.2: 1 ok, 0 warn, 1 blocked
1 plugin(s) will not load. Fix the blocked ones before upgrading.
```

Exit code `1` when anything is blocked, so it drops straight into CI or a shell
guard.

## Why this exists

On the day `0.2.0-rc.1` shipped, 11 of 22 third-party DSH plugins were reported
broken. The plugins were fine. Their `peerDependencies` said `^0.1.0-rc.6`, and
semver's caret rule makes that a range over **0.1.x only**:

```
^0.1.0-rc.6   →   >=0.1.0-rc.6  <0.2.0
```

That looks like it should admit `0.2.0-rc.2`, since a prerelease sorts below its
release. npm refuses it anyway, because of a second rule that most range
implementations miss:

> A version with a prerelease tag only satisfies a comparator set if some
> comparator in that set names the same `major.minor.patch` **with** a prerelease tag.

No comparator in `>=0.1.0-rc.6 <0.2.0` names `0.2.0`, so every `0.2.x`
prerelease is excluded — structurally, by the range, not by a bug in DSH.

The host now rejects such plugins at install and silently skips their bundle at
startup. What it does not do is tell you *in advance* which of your plugins this
happens to, or why. That is the gap `dshfit` fills.

## What it checks

| code | severity | meaning |
|---|---|---|
| `peer-range-excludes-host` | block | a `@deepseek-ai/dsh*` peer range does not admit the host version |
| `unparseable-peer-range` | block | the range cannot be parsed at all |
| `no-bundle` | block | no `dsh.bundle.patch`: installs, never mounts |
| `patch-missing-from-files` | block | `files[]` omits the patch the bundle points at |
| `patch-unreadable` | block | the patch has no readable `insert` block |
| `patch-name-mismatch` | block | a patch row inserts a name that is not the package |
| `declared-file-not-in-tarball` | block | *(tarball only)* `files[]` declares a path the archive does not contain |
| `tarball-entry-outside-package` | block | *(tarball only)* an archive entry resolves outside `package/` |
| `tarball-entry-unsafe` | block | *(tarball only)* an archive entry is a symlink, hardlink, or special file |
| `engines-dsh-excludes-host` | warn | author's declared support window excludes the host |
| `patch-row-without-id` | warn | a patch row cannot be targeted by a user config layer |

`engines.dsh` is a warning, never a block, because it is currently a declaration
that nothing in the host enforces. Blocking on it would reject plugins that load
fine today; hiding it would discard what the author explicitly wrote down.

### Tarball input

Point `check` at a `.tgz` (anything `npm pack` produced, or a downloaded registry
tarball) and the archive becomes the evidence, not the manifest's promise: three
checks can only run there.

```console
$ dshfit check --host 0.2.0-rc.2 ./dsh-openviking-2.0.1.tgz
BLOCKED  dsh-openviking@2.0.1  ./dsh-openviking-2.0.1.tgz  (tarball, files[] cross-checked against the archive)
  block declared-file-not-in-tarball dsh-openviking docs
        files[] declares "docs" but it is not in the archive; npm packed the package without it
        (.npmignore, a skipped prepare, or a stale entry), so whatever depends on that path will be
        missing after install
```

The argument is classified by content first (gzip magic) and name second, so a
tarball saved under another name is still read as one. Every entry is validated
before it is interpreted: paths that escape `package/`, absolute paths, and
links or special files are refused and reported, never normalised into
something safe-looking. The archive is read into memory — nothing is extracted to
disk, so a hostile entry has nowhere to land.

Two rules keep this from becoming a false-positive machine:

- Directory input never produces these codes. A checkout has no archive, so
  "declared but not packed" is not a fact about it.
- When the archive proves a `files[]` entry is merely stale (the patch is
  demonstrably inside it), the prediction is dropped rather than turned into a
  block. A tool that blocks a package you can see works teaches you to ignore it.

### Profile mode

`check` answers one plugin at a time. A real upgrade question is about the whole
profile, and a per-plugin listing buries the shape of the problem: five plugins
failing for the same reason read as five problems.

```console
$ dsh plugin --profile web list --json | dshfit profile --host 0.2.2-rc.1 -
web against host 0.2.2-rc.1 — 5 package(s)

blocked by root cause — 2 cause(s), 2 plugin(s)
  [block] peer-range-excludes-host  range=^0.2.0-rc.1  reason=prerelease-gate
    plugins (1): dsh-pet
    rejects (9):
      @deepseek-ai/dsh-agent-default-model
      ...
      @deepseek-ai/dsh-llm
    fix: the numbers already pass; only npm's prerelease gate refuses 0.2.2-rc.1. Add a
         clause naming that exact core with a prerelease tag — `|| >=0.2.2-rc.1` — which is
         the shape dsh-context uses for the 0.2.2 line. Re-run to confirm.
  [block] peer-range-excludes-host  range=^0.1.0-rc.6  reason=out-of-range
    ...
```

Three rules make the grouping trustworthy:

- **The key is code + range, not the plugin.** Nine peers that all say
  `^0.2.0-rc.1` are one mistake, so they are one row. The packages stay in it as
  the `rejects` list.
- **`out-of-range` and `prerelease-gate` never share a prescription.** They
  carry opposite instructions: widen the range, or make the range name the new
  core. One line saying "incompatible" would leave the reader to guess which.
- **What could not be read is its own section** and exits non-zero. `unknown` is
  not "ok", and it never becomes a compatibility cause: an unreadable manifest
  has no range to blame.

A package with no `dsh.bundle.patch` is reported as `not a plugin` rather than
blocked. In a profile that is what data and tooling packages look like — the real
`dsh-plugin-catalog` is one — and blocking them would train users to ignore the
blocked count. Pointed at directly, the same package is still a block, because
then the user is asserting it is a plugin.


## Install

Zero runtime dependencies, single static binary:

```console
$ go install github.com/atop0914/dshfit/cmd/dshfit@latest
```

## Usage

```console
$ dshfit check --host <version> [--json] <plugin-dir|package.tgz>...
$ dshfit profile --host <version> [--json] <listing.json|->
$ dshfit version
```

`--json` emits an array of reports (`plugin`, `version`, `dir`, `source`, `host`,
`verdict`, `issues[]`) for scripting. `dir` is whatever you passed — a directory
or an archive path — and `source` is `dir` or `tarball`, so a consumer can tell
which findings were verified against an artifact and which were inferred.

An unreadable input becomes a `manifest-unreadable` report instead of aborting
the run: a profile with one broken checkout (or one truncated `.tgz`) still gets
verdicts for everything else.

## Scope and honesty

What it does **not** do yet:

- **No registry fetching.** It reads a directory you point it at, a local
  `.tgz`, or the listing your profile's package manager already produced; it does
  not download from npm.
- **No fix suggestion.** It reports the failing range and the reason, but does
  not yet compute "pin `x@1.2.3`, which declares `^0.1.0-rc.6 || ^0.2.0-rc.1`".
  Profile mode names the shape of the edit, and says so as a candidate: only
  re-running confirms it.
- **No judgement of what a plugin does.** This tool answers "will it load". It
  does not audit capabilities, scripts, or network access — that is a different
  question with a different answer.
- **No full YAML parsing.** `cordis.patch.yml` is read by a targeted line
  scanner for `insert` rows. It refuses to guess: an unreadable patch is an
  error, not an empty result. A patch that hides inserts behind YAML features
  the scanner does not model (anchors, merge keys) will be reported as
  unreadable rather than silently passing.
- **`files[]` globs are not judged.** `lib/*.js` is a pattern over paths, not a
  path; treating its absence as a missing file would block packages that are
  fine. Only literal entries are cross-checked against the archive.
- **`declared-file-not-in-tarball` is a delivery check, not a load check.** A
  stale `files[]` entry naming a path the package never had will block too. That
  is deliberate — the alternative is silence, which is indistinguishable from a
  clean package — but it is the one place where the verdict is stricter than
  "will this load".
- **Range grammar is npm's, not node-semver's bug-for-bug.** It covers
  prefixes, x-ranges, hyphen ranges, unions, carets, tildes and the prerelease
  gate. It does not implement `npm:` aliases or git URLs.

## Design

See [docs/design.md](docs/design.md) for the layering and the reasoning behind
each severity choice.

## License

MIT
