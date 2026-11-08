# Design

## The one claim this tool makes

> A plugin that loaded yesterday can fail to load today without its author
> changing a line, because the *range* it declared was narrower than it looked.

Everything else follows from taking that seriously.

## Layering

```
cmd/dshfit          thin main: os.Exit(cli.Run(...))
internal/cli        argument parsing, exit codes, text and JSON rendering
internal/compat     verdicts: version gating + bundle structure + archive cross-checks
internal/manifest   what the bundle promises: a directory or an npm tarball
internal/semver     npm range algebra and the prerelease gate
```

`internal/semver` is pure: no filesystem, no environment, no clock. That is
deliberate. The interesting failures in this problem are all range-algebra
failures, and pure code lets each one be pinned by a table row instead of a
fixture.

`internal/manifest` is the only layer that touches the disk. It reads one
`package.json`, one patch file, and — when given a `.tgz` — the archive those two
came out of. It never infers: an unreadable patch is an error, not an empty list,
and an archive that is not rooted at `package/` is an error, not a guess about
which segment was the root.

## Why the prerelease gate is not optional

A checker that ignores the gate reports the exact opposite of the truth for the
most common upgrade in this ecosystem:

| host | `^0.1.0-rc.6` numerically | with the gate | correct |
|---|---|---|---|
| `0.1.5` | in range | in range | yes |
| `0.2.0-rc.2` | in range (`0.2.0-rc.2 < 0.2.0`) | **excluded** | excluded |

The second row is the whole reason `dshfit` exists. `internal/semver`
distinguishes the two rejection kinds:

- `out-of-range` — a comparator failed numerically. The fix is a wider range.
- `prerelease-gate` — every comparator passed numerically and npm still refused.
  The fix is a range that *names* the new core with a tag, e.g.
  `^0.1.0-rc.6 || ^0.2.0-rc.1`.

Collapsing those two into one message would tell users to widen a range that is
already wide enough, and the real fix would never be suggested.

## Why bundle structure is checked in the same pass

Version gating explains plugins that *visibly* stop working. Bundle structure
explains plugins that fail **silently**: if `files[]` omits the patch that
`dsh.bundle.patch` points at, the published tarball installs cleanly, appears in
the profile, and contributes nothing to the config tree. The symptom is "the
plugin does nothing", which sends users to the wrong place entirely.

Both are answerable from the same two files, so both belong in one report.

## Why the archive is a different witness

`files[]` is a promise. `package/patch-missing-from-files` is the prediction you
can make from that promise without an artifact: the patch is not in the list, so
the publish will not carry it. A `.tgz` replaces prediction with evidence, and
evidence can contradict the promise in both directions:

| what we observe | what it means |
|---|---|
| `files[]` names it, archive lacks it | the publish silently dropped something the manifest advertises |
| archive has it, `files[]` omits it | the declaration is stale; the package works |

The second row is why the tarball path does not simply re-run the directory
checks. A tool that blocks a package whose contents it can see, and which loads
fine, is a tool users learn to ignore — and the whole value here is that a block
means something. So when an archive is present, `patch-missing-from-files` is
dropped if the patch is demonstrably inside it, while
`declared-file-not-in-tarball` takes over the first row with better evidence.

The same asymmetry explains why these codes are silent for directory input. A
checkout has no archive; "declared but not packed" is not a fact about it, and
reporting the absence of evidence as a finding would block every directory the
user pointed at. `TestTarballModeIsNotDirectoryMode` pins that, because it is the
one property whose failure would be invisible in normal use and fatal to trust.

Untrusted input is handled by refusing rather than repairing. Escape paths,
absolute paths, drive letters, symlinks and special files are recorded and
skipped before anything is read from them; an archive with no `package/` prefix at
all is an error. Normalising `package/../evil` into `evil` would produce a
plausible-looking verdict about a package that is not the one on disk. Entry count
and unpacked size are capped, because the reader holds the archive in memory —
which is also why nothing is ever extracted: a hostile entry has nowhere to land.

## Why profile mode reads the package manager, not the directory

The temptation is to walk `<profile>/node_modules` and evaluate every directory
that has a `package.json`. That answer is wrong in a way that looks right: pnpm's
layout is a content-addressed store plus symlinks, so a walk sees hoisted copies
of transitive packages, sees the same package twice under different paths, and
cannot tell a declared plugin from something a plugin happened to pull in.

`dsh plugin --profile <name> list --json` is a passthrough to `pnpm list` inside
the profile directory — verified in `@deepseek-ai/dsh` 0.2.2-rc.1, where the
command forwards any argument it does not own to pnpm — so the listing is the
package manager's own account of what is declared and what version is installed.
Parsing it is not a convenience; it is the only source that matches the machine.

The listing is checked for shape rather than decoded tolerantly. Both wrong
inputs a user actually produces — `dsh plugin list` text, and a single
`package.json` — decode to "no projects" if the decoder merely tolerates them,
and an empty profile is indistinguishable from a clean bill of health.

## Why the grouping key is code + range

Profile mode exists to show the shape of a breakage, so the group is the unit of
the report. The key is the issue code and the range text.

**Not the plugin.** Measured on real data: `dsh-pet@0.4.5` rejects the 0.2.2 line
through nine different peers, all declaring `^0.2.0-rc.1`. Keyed per package that
is nine groups of one plugin each — a report that says "nine problems" about one
mistake. The packages stay in the group as the `rejects` list, which is where a
reader wants them.

**Not the rejection reason**, because within one run it is a function of the
range: the same range judged against the same host is rejected for the same
reason, so keying on it could never split a group. The first version of this key
did include it, and the mutation that removed it survived every test — which is
how the redundancy surfaced. The reason is still carried in the group, because it
decides the prescription, and *that* is where the two kinds are kept apart and
tested.

**Not normalised across range text.** `^0.2.0-rc.1` and `>=0.2.0-rc.1 <0.3.0`
describe the same set and stay in separate groups. Merging them would require
deciding that two different edits are the same edit, and a wrong merge hands
every plugin in the group one author's fix. Textual identity is provable;
semantic identity is a judgement call this tool does not need to make.

**The two rejection kinds stay apart in the prescription.** `out-of-range` and
`prerelease-gate` share the code `peer-range-excludes-host` and need opposite
edits: widen the range, versus make the range name the new core with a prerelease
tag. One line saying "incompatible" would leave the reader to pick a direction
and guess, so the fix text is asserted to differ and to say only its own thing.

## Why the fix solver re-checks its own output

The report answers "who falls and why". The next question is always "so what do I
write?", and a wrong answer there is worse than no answer: the user applies it,
still cannot install, and stops believing the tool. So the solver
(`internal/fix`) generates candidate rewrites and then puts each one back through
the same `internal/semver` call that produced the rejection. Candidates that fail
are dropped — never softened into "should work" — and the refusals are kept
alongside as evidence.

Three decisions follow from that:

**One engine, not two.** A second implementation of "does this range admit this
version" would agree with the first one until the day it did not, and the
disagreement would surface as a fix that does not fix. The solver owns no range
logic at all; it owns text edits and the discipline of checking them.

**The tagless shape is generated in order to be refused.** For a prerelease host
the solver also builds `R || >=0.2.2` — the append-the-release rewrite every
author reaches for. It always fails: `0.2.2-rc.1` sorts below `0.2.2`, and the
gate still wants a comparator naming `0.2.2` *with* a tag. Keeping it in the
generator is what makes "every candidate was checked" an assertion: delete the
check and that range shows up in the output as advice, so the tests go red.

**The rejection kind decides what is offered, not just what is said.** For
`prerelease-gate` the declared numbers were already right, so "throw the range
away" is not proposed at all — it would trade a nameable defect for an unnamed
one. For `out-of-range` it is proposed, last, with what it costs written next to
it.

What the printed range proves is narrow and the wording keeps it narrow: npm will
admit the host through it. Nothing about whether the plugin works on that host,
and nothing about versions that do not exist yet — which is why each candidate
carries the promise it makes (`>=host` claims every later release, `^host` claims
one line, `host` claims one version). The solver deliberately does not rewrite
the existing bound in place: "widen `<0.2.0` to `<0.4.0`" is a policy choice
about how much future to claim, and the tool can verify a candidate but cannot
choose for the author.

When the range does not parse, or the cause is not a range at all (a missing
patch file, an `engines.dsh` window), no candidate is produced and the group says
so. An empty list rendered as if it were a fix is the failure mode this project
exists to avoid.

## What profile mode refuses to judge

- **A package with no `dsh.bundle.patch`** is `not a plugin`, not a blocked one.
  Real profiles contain data and tooling packages (`dsh-plugin-catalog` ships
  catalogs), and the installer mounts nothing for them by design. Blocking them
  would make the blocked count meaningless. The same package pointed at directly
  is still a block: there the user is asserting it is a plugin.
- **A package whose manifest cannot be read** is `unknown`, counted separately,
  and exits non-zero. It is never folded into `ok` and never becomes a root
  cause: it has no range, so any compatibility attribution would be invented.
- **Two directories for one name** is `unknown`. Choosing one silently would
  report a verdict about a directory the host may not be loading.

## Severity rules

| situation | severity | why |
|---|---|---|
| peer range excludes host | block | the host refuses the bundle; it cannot load |
| no `dsh.bundle.patch` | block | nothing is ever mounted |
| patch missing from `files[]` | block | the tarball cannot carry the patch |
| patch name ≠ package name | block | cordis cannot resolve the entry |
| declared file absent from archive | block | the publish contradicts the manifest |
| archive entry escapes `package/` | block | extraction could write outside the package |
| archive entry is a link or special file | block | an extractor can be redirected through it |
| `engines.dsh` excludes host | **warn** | documented as unenforced declaration |
| patch row without `id` | **warn** | loads fine; only blocks config overrides |

The `engines.dsh` row is the one that took a decision. Treating it as a block
would reject plugins that demonstrably load on the host today. Treating it as
invisible would throw away the only place an author states their support window.
A warning keeps the information and keeps the exit code honest.

## Testing strategy

- **Table tests over ranges.** Each row is a claim about npm semantics, including
  the four rows that describe the 0.2 breakage verbatim.
- **Fixtures are real directories.** `compat` and `cli` tests write actual
  `package.json` + `cordis.patch.yml` trees to `t.TempDir()` and read them back,
  so a change in path handling fails a test instead of passing a mock.
- **Archives are really built.** The tarball tests write a gzip stream and a tar
  stream with `archive/tar` and `archive/gzip`, then read it through the same
  code the binary uses. A fabricated struct would let a regression in the reader
  pass unnoticed.
- **Reverse assertions.** Every "we report X" test is paired with a case that
  must *not* report X: directory input must never produce archive codes, and a
  package inside its own range must never be blocked. A checker that over-reports
  is worse than one that under-reports, because over-reporting is what teaches
  users to bypass it.
- **Mutations confirm the tests fail.** Batch 2 was checked by breaking the code
  five ways — escape detection made always-true, `..` detection removed, the
  directory guard deleted, file coverage made always-true, and an extra
  out-of-package entry injected — each turned a specific test red.
- **Exit codes are assertions.** The CLI contract (`0` ok, `1` blocked, `2` usage)
  is what makes the tool usable in CI, so it is tested directly.
- **No skips.** `make test-noskip` fails on `--- SKIP`. A skipped test in a
  checker is a claim with no evidence behind it.
- **The profile listing fixture is a real transcript.** `pnpm list --json` from
  pnpm 10.34.9 over five published packages, kept byte for byte, so the parser is
  tested against the shape that exists rather than the shape we would have
  invented. Its absolute paths are deliberately not required to exist: the test
  asserts parsing, not a machine.
- **Grouping is tested in both directions.** Twenty plugins with one cause must
  collapse into one row, and two causes with the same code must not: different
  reasons, and different range text, are each a separate test.
- **Fixes are re-checked from the outside.** Every solver test re-parses each
  returned range and asks the engine again, ignoring the `verified` field — the
  field is what the code claims, the re-check is what the engine says. The CLI
  test goes one step further and pulls the range back out of the printed text, so
  a suggestion that is only right in memory fails against the output users read.
  Both directions are covered: a range that should be offered must appear, and
  the tagless rewrite must appear only as refused.
- **The diff is tested on what must be absent.** The fixture pairs one plugin
  that drops, one that recovers and one that is blocked on both hosts; the test
  asserts the third name does not appear anywhere in the output. A diff that
  prints everything scores just as well on "does it mention the plugin that
  breaks", which is why the negative case is the one that carries the weight.

## Why a diff is not a verdict comparison

Two reports printed side by side cannot be compared by eye. The order, the
grouping and the prescriptions all differ, and the one line that matters —
`blocked → blocked` with the reason flipped from `prerelease-gate` to
`out-of-range` — is precisely the line that looks like no change.

So the change definition is written in the code instead of left to the reader:

1. **Loadability first.** "Not blocked on A, blocked on B" is a drop; the reverse
   is a recovery. That split decides which section a row lands in, because it is
   the question the user is asking.
2. **Then the diagnosis.** When loadability did not move, the comparison is
   `signature`: status, verdict, and the sorted `code(reason)` labels of every
   issue. Two sides with the same signature are not a difference and never reach
   the output. This is what keeps a pre-existing block from being blamed on the
   upgrade — the most damaging false positive this command could produce.
3. **Version is not in the signature.** A version bump that changes neither the
   verdict nor the causes is not something this tool has an opinion about, and
   printing it would teach the reader to skim the sections that matter.
4. **Presence is its own kind.** A package on one side only is reported with the
   state it arrives in, never as "worse": an addition is not a regression, and a
   data package that is not a plugin cannot break anything.

Three further decisions are worth recording because they are easy to get wrong:

- **The diff never judges a range.** It takes two finished `Result` values, so
  the CLI runs `Assess` twice. A second judge inside the diff would drift from the
  first, and the first symptom would be a diff that contradicts a plain run —
  an argument nobody can settle from the output.
- **Pairs are matched by name, never by index.** The two `Entries` slices happen
  to be sorted today, but sorting is `Assess`' internal choice; a diff that pairs
  entry 3 with entry 3 would start comparing different packages the moment either
  side gains one.
- **The exit code follows the target host, with one exception.** Something
  differs and B has a blocked or unjudged package exits `1`; nothing differs
  exits `0` even when packages are blocked on both hosts, because a diff answers
  "did the upgrade change anything that can break me". That exception is printed,
  not implied: the zero-diff output carries a note pointing at `profile` for the
  health question, since "no differences" alone reads like a clean bill.


## Next

1. Registry lookup: the solver now produces the smallest edit to a *range*; the
   remaining question is whether some published version already declares a range
   that admits the host, which needs `npm view` and a cache. The two answers are
   different — "write this" versus "install that" — and must not be conflated.
2. DSH plugin shell: a sidebar panel that runs the same Go core over stdio, so
   the matrix is visible where people actually upgrade.
3. CI mode: the same matrix as a GitHub Action comment, gated on the exit code
   the commands already define.
