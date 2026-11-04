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
| `engines-dsh-excludes-host` | warn | author's declared support window excludes the host |
| `patch-row-without-id` | warn | a patch row cannot be targeted by a user config layer |

`engines.dsh` is a warning, never a block, because it is currently a declaration
that nothing in the host enforces. Blocking on it would reject plugins that load
fine today; hiding it would discard what the author explicitly wrote down.

## Install

Zero runtime dependencies, single static binary:

```console
$ go install github.com/atop0914/dshfit/cmd/dshfit@latest
```

## Usage

```console
$ dshfit check --host <version> [--json] <plugin-dir>...
$ dshfit version
```

`--json` emits an array of reports (`plugin`, `version`, `dir`, `host`,
`verdict`, `issues[]`) for scripting.

An unreadable plugin directory becomes a `manifest-unreadable` report instead of
aborting the run: a profile with one broken checkout still gets verdicts for
everything else.

## Scope and honesty

This is the first slice. What it does **not** do yet:

- **No tarball fetching.** It reads directories you point it at; it does not
  pull from npm or a registry. Pair it with your installed profile.
- **No fix suggestion.** It reports the failing range and the reason, but does
  not yet compute "pin `x@1.2.3`, which declares `^0.1.0-rc.6 || ^0.2.0-rc.1`".
- **No full YAML parsing.** `cordis.patch.yml` is read by a targeted line
  scanner for `insert` rows. It refuses to guess: an unreadable patch is an
  error, not an empty result. A patch that hides inserts behind YAML features
  the scanner does not model (anchors, merge keys) will be reported as
  unreadable rather than silently passing.
- **Range grammar is npm's, not node-semver's bug-for-bug.** It covers
  prefixes, x-ranges, hyphen ranges, unions, carets, tildes and the prerelease
  gate. It does not implement `npm:` aliases or git URLs.

## Design

See [docs/design.md](docs/design.md) for the layering and the reasoning behind
each severity choice.

## License

MIT
