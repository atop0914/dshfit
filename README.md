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
| `unparseable-engines-dsh` | warn | `engines.dsh` is written but cannot be parsed |
| `patch-row-without-id` | warn | a patch row cannot be targeted by a user config layer |
| `manifest-unreadable` | block | the input could not be read as a plugin at all |

Every code in this table is in `internal/codes`, which is what the binary emits
from, and `internal/compat/evidence_test.go` holds a fixture that produces each
one. A code with no fixture, or a row whose severity disagrees with the code,
fails the test suite rather than reaching a user's terminal unexplained.

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
not a claim that some published version already declares it. That second
question is `--registry`, below.

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


### Registry: what the publisher ships

A profile run reads your disk, so it can only say "the version you have will not
load". The next question is always "is there one that will?", and that answer is
in the registry, not on the machine. `--registry` adds it — off by default,
because a tool whose job is checking an upgrade before you perform it has no
business opening a socket unless you said so:

```console
$ dshfit profile --host 0.2.2-rc.1 --registry testdata/profile/web/list.json
...
registry — releases the publisher ships
  dsh-narrow-range  could not be checked: "dsh-narrow-range": package not found
    in registry (the name is unpublished or private)
    nothing here says whether a released version would load
  dsh-pet  latest 0.4.8 does not admit 0.2.2-rc.1
    rejects 9 peer(s) declaring ^0.2.0-rc.1 (prerelease-gate): @deepseek-ai/dsh-agent-default-model,
    @deepseek-ai/dsh-commands, @deepseek-ai/dsh-credentials, and 6 more
    no published version admits 0.2.2-rc.1, out of 42 checked
    data as of 2026-11-12T12:39:53Z (from the registry)
```

Two sentences are deliberately different, and the difference is the point:

- **`does not admit`** — the registry answered. The newest release's own peer
  ranges were run through the same engine that judged your copy, and it refuses
  this host. With a numerator and a denominator (`out of 42 checked`) so the
  claim can be audited.
- **`could not be checked`** — the registry did not answer, or has no such name.
  Nothing is known, and the line says so instead of printing "no compatible
  release exists". A network failure that reads as a compatibility verdict is the
  one output this tool must never produce.

`admit` is not `will work`: it means npm's range check accepts the host for that
published version. Whether the plugin actually runs is still the author's claim,
which is why the local verdict stays the headline and this section only follows
it.

#### Cache and `--offline`

Registry data is cached (default 24h, `--cache-ttl`), one file per package **and
target host**: the packument is host-independent, but the file records the
question it answered, and a record that names a different host is refetched
rather than reused. The file also records *when* it was written, which is the
only reason a cache hit is not a lie:

```console
$ dshfit profile --host 0.2.2-rc.1 --registry --offline testdata/profile/web/list.json
  dsh-pet  latest 0.4.8 does not admit 0.2.2-rc.1
    no published version admits 0.2.2-rc.1, out of 42 checked
    data as of 2026-11-12T12:39:53Z (from cache, offline)
```

- `--offline` never touches the network. A cache miss is reported per package —
  `no cached registry data ... run once without --offline` — not silently
  skipped.
- When a refresh fails but an older copy exists, the old copy is printed with
  both its age *and* the failure (`refresh failed: HTTP 502`). Serving it as
  current data would be worse than not answering.
- A cache that cannot be written is one line of output, not a failed run: the
  answer already came from the registry.
- `--format markdown` does not grow a registry section. A CI summary of the
  local verdict must not start depending on the network.


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
$ dshfit version
dshfit v0.3.1 4f1c9ab 2026-11-20T10:12:33Z
```

`go install` runs no Makefile, so none of the `-ldflags` a `make build` passes
are applied: the binary derives its identity from the module it was built from
(`debug.ReadBuildInfo`) and prints only the fields it actually has. A local build
that knows neither a commit nor a date prints `dshfit <version>` instead of
`dev (unknown, unknown)`, and a binary whose identity cannot be established at
all says `dev` rather than inventing one.

`scripts/install-rehearsal.sh` (`make install-check`) is that command, run before
a release: it installs into an empty `GOBIN` and runs the first example in this
document from a directory containing nothing but a plugin, so "the README's
install line works" is checked by running it rather than by reading it. It needs
the module proxy, which is why it is not part of `make ci`.

## Usage

```console
$ dshfit check --host <version> [--json] <plugin-dir|package.tgz>...
$ dshfit profile --host <version> [--json|--format text|markdown] [--registry [--offline]] <listing.json|->
$ dshfit diff --from <version> --to <version> [--json] <listing.json|->
$ dshfit serve
$ dshfit version
$ dshfit help
```

`--registry` flags for `profile` (all optional, none of them on by default):

| flag | meaning |
|---|---|
| `--registry` | also ask npm which published releases accept the host |
| `--offline` | cached registry data only; implies `--registry`, never dials |
| `--cache-dir` | where the cache lives (default `<user cache>/dshfit/registry`) |
| `--cache-ttl` | how long a cached packument counts as current (default 24h; a negative value refetches every run) |
| `--registry-url` | registry base URL (default `https://registry.npmjs.org`) |

The registry answer is an addition to the report, never a substitution: exit
status still means "will this profile load", so a network failure cannot turn a
blocked profile into a passing one.

`--format markdown` is the same answer, laid out for a CI step summary: a
one-line conclusion, one table row per root cause with the smallest edit that
fixes it, the packages that could not be read in their own table, and the
plugins that were verified clean. Every number in it comes from `counts` and
every cause from `groups`, so it cannot disagree with the terminal output.
`--json` and `--format` asking for different things exits 2 rather than letting
one win silently.

`--json` emits an array of reports (`plugin`, `version`, `dir`, `source`, `host`,
`verdict`, `issues[]`) for scripting. `dir` is whatever you passed — a directory
or an archive path — and `source` is `dir` or `tarball`, so a consumer can tell
which findings were verified against an artifact and which were inferred.

With `--registry`, each blocked entry also carries a `registry` object:
`latest`, `latest_admits`, `latest_rejections[]` (`package`, `range`, `reason`,
`detail`), `admitting[]`, `published`, `unparseable`, `as_of`, `from`, `stale`,
`refresh_error`, `cache_error`, and `error`. `error` means the lookup failed and
nothing is known — reading it as "no compatible release" is the mistake this
field exists to prevent. Every rejection there was computed by the same engine
that produced the local verdict, so the two halves of one report cannot
disagree about the same range.

`diff --json` emits one object instead: `from`, `to`, `projects`, `changes[]`
(`kind`, `name`, `from`, `to`, and both sides' `status` / `verdict` / `causes`),
plus `counts` and the target host's own `to_counts`, so a consumer that only sees
the diff still learns that three other packages are blocked on both hosts.
Two runs over the same pair produce byte-identical JSON.

An unreadable input becomes a `manifest-unreadable` report instead of aborting
the run: a profile with one broken checkout (or one truncated `.tgz`) still gets
verdicts for everything else.

## CI

The panel answers "will this drop when I upgrade". CI answers the other
question: *did someone change a range and nobody noticed*. A compatibility
regression caught before merge never reaches a user's machine.

```yaml
- uses: actions/checkout@v4
- uses: actions/setup-go@v5
  with: { go-version: "1.25", cache: false }
- run: go build ./cmd/dshfit
- name: Would this profile survive the upgrade?
  run: |
    dshfit profile --host 0.3.0 --format markdown list.json >> "$GITHUB_STEP_SUMMARY"
```

The exit code is the contract, and it has three values because two of them mean
different things to a person reading a red cross:

| code | meaning | what a job should do |
|---|---|---|
| 0 | nothing blocked, nothing unread | pass |
| 1 | a plugin will not load, **or** a package could not be judged | show the report; a verdict was reached |
| 2 | usage or configuration error | fail loudly — no verdict was reached |

Collapsing 1 and 2 into one failure sends whoever reads the job to the wrong
place: they look for a range problem when the real problem is a typo in `--host`.

**Why a step summary and not a PR comment.** On a pull request from a fork the
`GITHUB_TOKEN` is read-only, so posting a comment needs write access the
workflow does not have and returns `403`. A job that goes red on a permission
error looks like the tool is broken, and the usual response to a gate that
looks broken is to switch it off. `$GITHUB_STEP_SUMMARY` needs no token and
shows the same markdown on the run page.

**Gate on the verdict, not just the exit code.** `scripts/ci-profile-matrix.sh`
runs the fixture matrix (a clean profile, a drifting one, and a real-world
snapshot, each against two harness versions), writes the markdown to the step
summary, and fails when the counts contradict the table inside the script. A
blocked plugin is the *expected* answer for those fixtures — the job fails on a
count that moved, not on the fact that something is blocked, because a gate that
is red on every run is a gate nobody reads. It requires `jq` (present on GitHub
runners); the table is a measured fact about the fixtures, so when a change is
meant to move one, that row moves in the same commit.

### Fuzzing

Two of this tool's inputs are written by people who are not here: the peer range
strings in someone's `package.json`, and the packument the registry returns. A
panic on either is not one wrong verdict — it is the other nineteen plugins losing
theirs, in a run that prints nothing at all.

```console
$ make fuzz                # 10s per target, four targets
$ make fuzz FUZZTIME=2m
```

The assertions are the promises above, not "does not crash":

| target | promise |
|---|---|
| `FuzzParseRange` | arbitrary bytes may not panic; a failed parse leaves no half range behind |
| `FuzzExplain` | a rejection always names a clause and a reason, and repeats identically |
| `FuzzRegistryParse` | a successful parse always has ≥1 parseable version — an empty list is never an answer |
| `FuzzCacheRecord` | a corrupt cache file is a miss to refetch, never a crash and never "no cache" |

`make fuzz` is deliberately absent from `make ci`. A fuzz run finds inputs by
chance: a pass proves nothing about what it did not try, and a job that goes red at
a random minute on an input nobody kept is a job people switch off. What CI runs is
the corpus — the seeds inside each target plus anything under `testdata/fuzz/` —
because those are ordinary regression tests. When the fuzzer does fail, it writes
the input there; that file is the evidence and it must be committed.

### pre-commit

Same command, run before the commit instead of after the push:

```console
$ dsh plugin --profile web list --json > .dshfit-listing.json   # once
$ DSHFIT_HOST=0.3.0 sh scripts/pre-commit.sh                    # 0 ok, 1 blocked, 2 misconfigured
$ git config core.hooksPath .githooks && ln -s ../../scripts/pre-commit.sh .githooks/pre-commit
```

The script prints the same report and the smallest edit, and **does not install
itself** — writing into `.git/hooks` is a change to your repository, so it is
yours to make. When it exits 1 it says so and names the escape hatch
(`--no-verify`); a hook that blocks silently teaches people to bypass it.

### Rehearsed commands

Every command in this document is executed by `scripts/readme-rehearsal.sh`
(`make readme`) against the fixtures in `testdata/`, with **the exit code as the
assertion** — not "it printed something that looked right". A clean profile must
exit 0, a blocked one 1, a typo 2, and a `blocked → blocked` diff that changes
its cause must still be a difference.

It dials nothing. The registry section is rehearsed through `--offline` and a
seeded cache directory whose `--registry-url` points at a closed port: a step
that opened a socket would print "could not be checked" instead of the cached
answer and fail. That also pins the cache key — the seeded file is named from a
hash of package, host and registry URL, so if the key stops covering the URL the
file stops matching and the rehearsal breaks.

Two more gates keep this document from drifting:

- `internal/docs` compares the code table above with `internal/codes` row by row,
  including each severity and the `(tarball only)` marker.
- The same check fails on a relative link or a backticked repository path that
  does not exist, and on a subcommand this README never names.

## Scope and honesty

What it does **not** do yet:

- **No registry fetching unless you ask for it.** A plain run reads a directory
  you point it at, a local `.tgz`, or the listing your profile's package manager
  already produced, and opens no socket. `--registry` (and `--offline` for the
  cache alone) is the one exception, and it fetches packuments — metadata — only:
  it never downloads a tarball, installs anything, or resolves dependencies.
- **No fix suggestion derived from the registry.** It computes the smallest edit
  to the range and proves npm will accept it, and with `--registry` it can say
  which released version admits the host. It does not combine the two into
  "upgrade to `x@1.2.3`, which already declares `^0.1.0-rc.6 || ^0.2.0-rc.1`",
  because that is a different question — a version that admits the host — and
  answering it half-way would look like a recommendation.
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
