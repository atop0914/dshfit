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
    smallest fix: ^0.2.0-rc.1 || >=0.2.2-rc.1
      the added clause repeats 0.2.2-rc.1 with its prerelease tag, which is what opens npm's
      gate; a clause without that tag is refused even though the numbers pass
    other verified fixes:
      ^0.2.0-rc.1 || ^0.2.2-rc.1
        claims ^0.2.2-rc.1 — the host's own line only, instead of every release after it
      ^0.2.0-rc.1 || 0.2.2-rc.1
        pins exactly 0.2.2-rc.1: this release and nothing after it, so the next one needs
        another edit
    not a fix: ^0.2.0-rc.1 || >=0.2.2
      0.2.2-rc.1 is a prerelease of 0.2.2, and no comparator in ">=0.2.0-rc.1 <0.3.0" names
      0.2.2 with a prerelease tag; npm only admits prereleases that the range explicitly
      opts into
  [block] peer-range-excludes-host  range=^0.1.0-rc.6  reason=out-of-range
    ...
    smallest fix: ^0.1.0-rc.6 || >=0.2.2-rc.1
```

`smallest fix` is not a guess. Every range printed there was re-parsed and
re-tested against the host by the same engine that produced the rejection, and a
candidate that fails is dropped rather than softened. The `not a fix` entry is
the shape authors actually reach for — appending the *release* `0.2.2` — kept in
the output because its refusal is the explanation, not noise.

What each candidate promises is printed with it, because a range edit is a
promise about versions that do not exist yet:

| shape | what it says |
|---|---|
| `R \|\| >=host` | keep what you declare, add the host **and everything after it** |
| `R \|\| ^host` | keep what you declare, add the host's own line |
| `R \|\| host` | keep what you declare, add exactly this version |
| `>=host` | drop what you declare (offered only for a numeric shortfall) |

Nothing here consults the registry, so this is the smallest edit to the *range*,
not a claim that some published version already declares it.

Four rules make the grouping trustworthy:

- **The key is code + range, not the plugin.** Nine peers that all say
  `^0.2.0-rc.1` are one mistake, so they are one row. The packages stay in it as
  the `rejects` list.
- **`out-of-range` and `prerelease-gate` never share a prescription.** They
  carry opposite instructions: widen the range, or make the range name the new
  core. One line saying "incompatible" would leave the reader to guess which.
- **What could not be read is its own section** and exits non-zero. `unknown` is
  not "ok", and it never becomes a compatibility cause: an unreadable manifest
  has no range to blame.
- **A fix is only printed if it verifies.** The solver generates candidate
  rewrites, re-parses each one, and asks the same engine whether the host passes;
  a candidate that fails never reaches the output. When nothing can be computed —
  an unparseable range, or a cause that is not about ranges at all — the group
  says so instead of offering a plausible-looking range.


A package with no `dsh.bundle.patch` is reported as `not a plugin` rather than
blocked. In a profile that is what data and tooling packages look like — the real
`dsh-plugin-catalog` is one — and blocking them would train users to ignore the
blocked count. Pointed at directly, the same package is still a block, because
then the user is asserting it is a plugin.


### Version diff

`profile` answers "what does this profile do on B". The question right before it
is "what changes if I move from A to B", and running both and comparing two
reports by eye does not work: the order, the grouping and the prescriptions all
differ between them.

```console
$ dshfit diff --from 0.2.2-rc.1 --to 0.3.0 testdata/profile/web/list.json
web · host 0.2.2-rc.1 → 0.3.0 — 5 package(s), 1 differ

root cause changed — 1
  dsh-pet@0.4.5  blocked → blocked
    before: peer-range-excludes-host(prerelease-gate)
    after:  peer-range-excludes-host(out-of-range)

5 package(s) on 0.3.0: 1 ok, 0 warn, 2 blocked, 1 unknown, 1 not a plugin
`dshfit profile --host 0.3.0` prints the cause and the smallest fix for each block.
1 package(s) could not be judged on 0.3.0; that is not the same as compatible.
```

That is the case a verdict comparison loses. Both hosts refuse `dsh-pet`, so
`blocked → blocked` looks like no change — but the prescription flipped from
"make the range name the new core" to "widen the range", and those two edits are
not interchangeable. `dsh-narrow-range` is out of range on both hosts, so it is
not in the report at all.

What counts as a difference, in the order the sections are printed:

| section | meaning |
|---|---|
| `will stop loading` | loads on A, blocked on B |
| `improves` | blocked on A, loads on B (or loses a warning) |
| `root cause changed` | loadability unchanged, diagnosis differs — including a new warning |
| `only in the target / source listing` | present on one side only, with the state it arrives in |

A package that behaves identically on both hosts never appears: a block that
predates the upgrade is not caused by it, and putting it in the report is how a
diff stops being readable. Both `--from` and `--to` are required — a default on
either side would answer a question about a version nobody named.

Exit codes answer "did the upgrade change anything that can break me", not "is
the target host healthy": nothing differs is `0` even when packages are blocked
on both hosts (the footer says so and points at `profile`), and something differs
with a blocked or unjudged package on B is `1`. Packages that are only in the
source listing never fail the run — they are gone after the upgrade.


### Panel sidecar

A panel inside DSH cannot run a terminal command and parse a report. `serve`
gives it the same three answers over a pipe: one JSON request per line on stdin,
one JSON response per line on stdout, nothing else.

```console
$ echo '{"id":1,"method":"profile","params":{"host":"0.2.2-rc.1","listing":"list.json"}}' | dshfit serve
{"id":1,"result":{"host":"0.2.2-rc.1","projects":["web"],"entries":[...],"groups":[...],"counts":{...}}}
```

| method | params | result |
|---|---|---|
| `profile` | `host`, `listing`, `base`? | a `profile` result, as `profile --json` |
| `diff` | `from`, `to`, `listing`, `base`? | a `diff` result, as `diff --json` |
| `version` | — | `name`, `version`, `protocol` |

`listing` is the listing text itself or a path to one. A bare `-` is refused:
stdin carries requests here, so "read the listing from stdin" would consume the
protocol stream. `base` is the directory relative package paths in an inline
listing resolve against; a path already answers that question and ignores `base`.

Four rules the caller can rely on, each of which is the difference between a
panel and a wrapper that looks like one:

- **A bad line is answered, not fatal.** A malformed request, an unknown method,
  or an unparseable host gets `{"id":N,"error":{"code":"...","message":"..."}}`
  and the session continues. `code` is one of `bad-request`, `unknown-method`,
  `invalid-input`, `internal` — an unknown method is never an empty result,
  because an empty result and "asked, nothing wrong" are the same bytes.
- **Responses pair by id, not by order.** Requests are judged concurrently, so a
  response may arrive in any order; the id it echoes is the only pairing key.
  Lines never interleave: one writer owns stdout. A request with no id is
  rejected, since its response could never be matched.
- **Diagnostics go to stderr.** Nothing but protocol is written to stdout, so a
  `println` cannot corrupt the stream a client is parsing.
- **Blocked is still exit 0.** The verdicts belong to individual responses; the
  exit code of a long-lived process has no single answer to carry. Only a broken
  stdout exits non-zero, because then nothing can be reported at all.

Requests are capped at 8 MiB per line. An over-long line is discarded to its
newline and answered with an error, so the session stays on a request boundary —
send big listings as a path instead.


## Install

Zero runtime dependencies, single static binary:

```console
$ go install github.com/atop0914/dshfit/cmd/dshfit@latest
```

## Usage

```console
$ dshfit check --host <version> [--json] <plugin-dir|package.tgz>...
$ dshfit profile --host <version> [--json] <listing.json|->
$ dshfit diff --from <version> --to <version> [--json] <listing.json|->
$ dshfit serve
$ dshfit version
```

`--json` emits an array of reports (`plugin`, `version`, `dir`, `source`, `host`,
`verdict`, `issues[]`) for scripting. `dir` is whatever you passed — a directory
or an archive path — and `source` is `dir` or `tarball`, so a consumer can tell
which findings were verified against an artifact and which were inferred.

`diff --json` emits one object instead: `from`, `to`, `projects`, `changes[]`
(`kind`, `name`, `from`, `to`, and both sides' `status` / `verdict` / `causes`),
plus `counts` and the target host's own `to_counts`, so a consumer that only sees
the diff still learns that three other packages are blocked on both hosts.
Two runs over the same pair produce byte-identical JSON.

An unreadable input becomes a `manifest-unreadable` report instead of aborting
the run: a profile with one broken checkout (or one truncated `.tgz`) still gets
verdicts for everything else.

## Scope and honesty

What it does **not** do yet:

- **No registry fetching.** It reads a directory you point it at, a local
  `.tgz`, or the listing your profile's package manager already produced; it does
  not download from npm.
- **No fix suggestion from the registry.** It computes the smallest edit to the
  range and proves npm will accept it, but it does not look up what is published,
  so it cannot say "pin `x@1.2.3`, which already declares `^0.1.0-rc.6 ||
  ^0.2.0-rc.1`". For that you still need the registry, and a version that admits
  the host is a different question from a range that does.
- **No judgement of what a plugin does.** This tool answers "will it load". It
  does not audit capabilities, scripts, or network access — that is a different
  question with a different answer.
- **`diff` moves the host, not the profile.** One listing is read once and judged
  twice, so the comparison is exactly "same plugins, two harness versions". A
  package that exists on one side only is reported as such, but a diff cannot
  tell you that you also upgraded a plugin — that is a profile change, and it
  needs two listings.
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
