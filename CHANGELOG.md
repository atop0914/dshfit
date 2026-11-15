# Changelog

`dshfit` answers one question — *which of my plugins will stop loading when the
host moves to version X, and what is the smallest edit that fixes it* — so the
sections below are grouped by the capability a user can observe, not by commit.
Every entry names the boundary next to the capability: a tool that only says what
it does is a tool whose gaps you discover on the day you need it.

## v1.0.0 — 2026-11-15

First release. Everything below is exercised by a gate that can fail
(`make ci`, `make readme`, `make release-check`), and the limits are the ones the
binary actually enforces, not intentions.

### A verdict before the upgrade, one plugin at a time

`dshfit check --host <version> <plugin-dir|package.tgz>...` renders `OK`, `WARN`
or `BLOCKED` per plugin and explains a block by naming the failing clause and the
rule that rejected it, instead of the word "incompatible".

```console
$ dshfit check --host 0.2.0-rc.2 ./plugins/dsh-openviking
BLOCKED  dsh-openviking@2.0.1  ./plugins/dsh-openviking
  block peer-range-excludes-host  @deepseek-ai/dsh-app-boot ^0.1.0-rc.6
        0.2.0-rc.2 is a prerelease of 0.2.0, and no comparator in
        ">=0.1.0-rc.6 <0.2.0" names 0.2.0 with a prerelease tag; npm only admits
        prereleases that the range explicitly opts into
```

- The range algebra is npm's: prefixes, x-ranges, hyphen ranges, unions, carets,
  tildes, and the prerelease gate as a rule of its own. **Boundary: no `npm:`
  aliases and no git URLs** — those are not ranges, and guessing at them would
  produce verdicts nobody can check.
- `out-of-range` and `prerelease-gate` are two rejections with opposite
  prescriptions (widen the range / make the range name the new core) and are
  never collapsed into one "incompatible" line.
- `engines.dsh` is a warning, never a block, because nothing in the host enforces
  it today. Hiding it would discard the only support window the author wrote
  down; blocking on it would reject plugins that load fine this morning.
- Exit codes carry the verdict: `0` nothing blocked, `1` something will not load,
  `2` the command itself was wrong. Two failures, two destinations — a typo in
  `--host` must not send the reader hunting for a range problem.

### The archive as a second witness

Point `check` at a `.tgz` (anything `npm pack` produced, or a downloaded registry
tarball) and the archive becomes the evidence rather than the manifest's promise.
Three codes exist only here: `declared-file-not-in-tarball`,
`tarball-entry-outside-package`, `tarball-entry-unsafe`.

- The argument is classified by content first (gzip magic) and by name second.
- Every entry is validated before it is interpreted: paths escaping `package/`,
  absolute paths, symlinks, hardlinks and special files are refused and reported,
  never normalised into something that looks safe.
- The archive is read in memory; nothing is extracted, so a hostile entry has
  nowhere to land. Entry count and total inflate size are capped.
- Two rules keep this from becoming a false-positive machine: directory input
  never produces these codes, and when the archive proves a `files[]` entry is
  merely stale, the prediction is dropped instead of promoted to a block.
- **Boundary: `files[]` globs are not judged.** `lib/*.js` is a pattern over
  paths, and treating its absence as a missing file would block packages that are
  fine. Only literal entries are cross-checked.
- **Boundary: `declared-file-not-in-tarball` is a delivery check, not a load
  check.** A stale `files[]` entry naming a path the package never had will also
  block. That is deliberate — the alternative is silence, which is
  indistinguishable from a clean package — but it is the one place where the
  verdict is stricter than "will this load".

### A whole profile, grouped by root cause

`dshfit profile --host <version> <listing.json|->` reads what the package manager
already produced (`dsh plugin --profile web list --json`, which is `pnpm list
--json`), judges every package, and groups the blocks by **code + range**.

- Nine peers that all say `^0.2.0-rc.1` are one mistake, so they are one row; the
  packages stay in it as the `rejects` list. Adding the package name to the key
  would print one mistake nine times, and adding the reason is dead weight —
  inside a single run the reason is a function of the range.
- `unknown` is its own section and exits non-zero. It is not "ok", and it never
  becomes a compatibility cause: an unreadable manifest has no range to blame.
- A package with no `dsh.bundle.patch` is `not a plugin` in a profile (that is
  what data and tooling packages look like) and a block when pointed at directly
  (then the user is asserting it is a plugin).
- **Boundary: no full YAML parsing.** `cordis.patch.yml` is read by a targeted
  line scanner for `insert` rows; a patch that hides inserts behind anchors or
  merge keys is reported as unreadable rather than silently passing.

### Fixes that prove themselves

Every `smallest fix` was re-parsed and re-tested against the host by the same
engine that produced the rejection. A candidate that fails is dropped, never
softened into "should work".

- Four shapes, ordered by how much of the author's declaration they destroy:
  `R || >=host`, `R || ^host`, `R || host`, and `>=host` (offered only for a
  numeric shortfall). Each prints the promise it makes, because a range edit is a
  promise about versions that do not exist yet.
- The shape authors actually reach for — appending the *release* — is kept in the
  output under `not a fix`, with its refusal: that refusal is the explanation.
- **Boundary: the upper bound is not raised in place.** Whether `<0.2.0` should
  become `<0.3.0` or `<0.4.0` is the author's policy, not a computation. The tool
  can verify a candidate; it cannot choose one for them.
- **Boundary: a fix is the smallest edit to the range, not a claim that some
  published version already declares it.** That second question is `--registry`.

### What changes between two hosts

`dshfit diff --from A --to B` reads one listing once and judges it twice, then
prints only the differences.

- `blocked -> blocked` with a changed reason **is** a difference, and it is the
  case a verdict comparison loses: the prescription flips from "make the range
  name the new core" to "widen the range", and those edits are not
  interchangeable.
- A package that behaves identically on both hosts never appears: a block that
  predates the upgrade is not caused by it.
- Both `--from` and `--to` are required — a default on either side would answer a
  question about a version nobody named. Two runs over the same pair produce
  byte-identical JSON.
- **Boundary: `diff` moves the host, not the profile.** A package present on one
  side only is reported as such, but telling you that you also upgraded a plugin
  needs two listings, not one.

### The same three answers over a pipe

`dshfit serve` gives a panel what a terminal report cannot: one JSON request per
line on stdin, one JSON response per line on stdout, nothing else.

- Methods `profile`, `diff`, `version`. **The protocol repeats no judgement** —
  it serialises what the CLI already computed, so the two cannot disagree.
- A malformed line, an unknown method or an unparseable host is answered with a
  structured `error` and the session continues. An unknown method is never an
  empty result: an empty result and "asked, nothing wrong" are the same bytes.
- Responses pair by `id`, not by arrival order; a request without an id is
  rejected because its response could never be matched. One writer owns stdout,
  so lines never interleave, and diagnostics go to stderr.
- Requests are capped at 8 MiB per line; an over-long line is discarded to its
  newline and answered, so the session stays on a request boundary.
- Blocked is still exit 0 here: the verdict belongs to each response, and a
  long-lived process has no single answer to carry.

### A panel view model that cannot contradict its input

`plugins/dshfit-panel` (TypeScript, private, not published) holds the rendering
half: `render.ts` turns a `ProfileResult` into summary numbers, blocks grouped by
cause, and the unjudged packages in their own row.

- Every number comes from `counts`; the renderer never counts entries itself.
  `integrity()` refuses to answer when `counts` contradicts the lists it claims to
  describe (`tone=incomplete`), because that is a report that contradicts itself,
  not a layout problem.
- `unknown > 0` with `blocked == 0` is not `clear`, and its copy never says "none
  blocked". `clear` is reachable only when every package was judged, nothing is
  blocked or warned, and at least one plugin was seen.
- `client.ts` registers the sidebar slot by copying the published
  `dsh-client-ui-cordis` convention (`ctx.slots.inject(...)` plus a `dsh.client`
  block in `package.json`).
- **Boundary: the panel draws, but it cannot fetch.** There is no transport
  between the browser half and the host sidecar — the host registers no Remote
  endpoint — so the default loader fails with `no-transport` instead of returning
  an empty profile. An empty profile would render as "0 blocked", which is the
  one lie this project must not tell.

### The same answer as a CI step summary

`profile --format markdown` is a second **renderer**, not a second judgement:
numbers come from `counts`, causes from `groups`, so it cannot disagree with the
terminal output.

- `scripts/ci-profile-matrix.sh` runs a fixture matrix (a clean profile, a
  drifting one, a real-world snapshot, each against two hosts), writes the
  markdown to `$GITHUB_STEP_SUMMARY`, and fails when a count stops matching the
  measured table. A blocked plugin is the *expected* answer there — the job fails
  on a count that moved, not on the fact that something is blocked. Exit 2 (bad
  configuration) travels on its own `::error::` channel.
- `scripts/pre-commit.sh` runs the same command before a commit, and **does not
  install itself**: writing into `.git/hooks` is a change to your repository, so
  it is yours to make. When it exits 1 it names `--no-verify`.
- Why a step summary and not a PR comment: on a fork PR the `GITHUB_TOKEN` is
  read-only, so a comment returns 403, and a gate that goes red on a permission
  error looks broken — and a gate that looks broken gets switched off.

### What the publisher ships

`--registry` adds the second question (*is there a released version that will
load?*) and is off by default: a tool whose job is checking an upgrade **before**
you perform it has no business opening a socket unless you said so.

- Three sentences that are never merged: `admits` / `does not admit` with a
  numerator and denominator (`out of 42 checked`) / `could not be checked` with
  the reason. A network failure that reads as a compatibility verdict is the one
  output this tool must never produce.
- The packument is reduced to `{version, peers, engines}`; the answer is cached
  per package **and target host** (the packument is host-independent, but the
  file records the question it answered) with a TTL, an atomic write, and
  `fetched_at` verified on read. A timestamp from the future is not fresh.
- `--offline` never dials. A cache miss is reported per package with the command
  that fixes it, never skipped. When a refresh fails but an older copy exists,
  the copy is printed with both its age and the failure.
- **Boundary: metadata only.** It never downloads a tarball, installs anything,
  or resolves dependencies, and it does not yet turn the admitting version list
  into "install that".
- **Boundary: `admit` is not `will work`.** It means npm's range check accepts the
  host for that published version; whether the plugin runs is still the author's
  claim.

### Hardening: the inputs are written by people who are not here

- Four fuzz targets (`FuzzParseRange`, `FuzzExplain`, `FuzzRegistryParse`,
  `FuzzCacheRecord`) assert the promises above rather than "does not crash": an
  arbitrary byte string may not panic, a failed parse leaves no half struct
  behind, a successful packument parse always has at least one parseable version,
  and a corrupt cache file is a miss to refetch — never "no cache".
- `make fuzz` is deliberately absent from `make ci`: a fuzz run finds inputs by
  chance, and a job that goes red at a random minute on an input nobody kept is a
  job people switch off. What CI runs is the corpus — the seeds plus every file
  under `testdata/fuzz/` — and a failing input must be committed, because that
  file is the evidence and it keeps working after the fuzzer stops.
- Six property tests, with expectations written in an external table rather than
  read back out of the code. The first version derived the upper bound from the
  parsed comparator, so flipping `<` to `<=` kept the test green.
- This is what found the release-blocking bug of the cycle: `^9223372036854775807.0.0`
  incremented its major into a negative number, the range then excluded its own
  lower bound, and a blocked plugin would have been reported as fine. Six
  increment paths now share `incMajor/incMinor/incPatch`; an unrepresentable
  upper bound is dropped (it is above every version) and an unrepresentable lower
  bound becomes `> topVersion` (nothing satisfies it) — never an empty comparator,
  which means *any*.

### Documentation as an executable claim

- `scripts/readme-rehearsal.sh` (`make readme`, in CI) runs every command the
  README shows against the fixtures with **the exit code as the assertion**, and
  dials nothing: the registry section is rehearsed through `--offline` and a
  seeded cache whose `--registry-url` points at a closed port. A step that opened
  a socket would print "could not be checked" and fail.
- `scripts/install-rehearsal.sh` (`make install-check`) installs the module the
  way a user does — `go install ...@<ref>` into an empty `GOBIN` — and asserts
  the version string is a real identity, then runs the README's first example in
  a directory containing nothing but a plugin. `go install` runs no Makefile, so
  none of the `-ldflags` apply; the binary derives its identity from
  `debug.ReadBuildInfo` and prints only the fields it has.
- `internal/codes` is the single registry of the 13 issue codes (severity, scope,
  one-line meaning), and `internal/compat/evidence_test.go` iterates over it, so
  a code added without a fixture fails the suite instead of reaching a terminal
  unexplained.
- `internal/docs` compares the README's code table against that registry in both
  directions (set, severity, the `(tarball only)` marker), resolves every relative
  link, checks every backticked repository path, and checks that every subcommand
  is named. All four rules **fail closed**: an unreadable README is an error, not
  an empty problem list. The previous generation of this check returned "clean"
  for any tree, and was quoted as evidence.
- `scripts/release-check.sh` (`make release-check`) is the last gate before
  publishing: the tag, the CHANGELOG's newest section and the identity the built
  binary reports must all be the same version, and the tag must point at `HEAD`.

### Install

```console
$ go install github.com/atop0914/dshfit/cmd/dshfit@v1.0.0
$ dshfit version
dshfit v1.0.0
```

Zero runtime dependencies (one module in `go mod graph`, gated by
`make deps-check`), single static binary, `CGO_ENABLED=0`. Built and checked for
linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64.

### Not in this release

- **The panel's transport.** The browser half and the view model exist; the
  registration convention on the host side (`dsh-api-remotes`) has not been read,
  and guessing at a protocol is how a panel ends up rendering nothing.
- **`install that`.** The registry knows which released versions admit the host;
  turning that into "upgrade to this one" also needs the archive check on
  `dist.tarball` to be honest about what that version contains.
- **Capability auditing.** This tool answers "will it load". What a plugin does
  with the capabilities it declares is a different question with a different
  answer, and it belongs to `dsh-plugin-gate`.
