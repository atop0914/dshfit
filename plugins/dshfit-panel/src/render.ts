/**
 * The panel's view model, and the only place that decides what a reader is told.
 *
 * Two rules shape this file, and both of them exist because the opposite choice
 * produces a screen that lies:
 *
 * Every number shown comes from `counts`. Nothing here walks `entries` to count
 * anything. A renderer that re-counts is a second opinion about the same data,
 * and the failure mode is quietly absurd — a summary saying three plugins are
 * blocked above a list showing two — with no way for a reader to know which half
 * to believe. `integrity()` therefore treats a disagreement between the counts
 * and the lists they are supposed to describe as a refusal to answer, not as a
 * cosmetic glitch.
 *
 * "Nothing is blocked" is not the same as "everything is fine". A package dshfit
 * could not read is unknown, and unknown renders as unknown. The clear verdict is
 * reachable only when every package in the listing was judged, nothing blocks,
 * nothing warns, and nothing is unknown. This is the same rule the Go core
 * follows for exit codes; if the panel softened it, a user would upgrade on the
 * strength of a green sentence that was never earned.
 *
 * Kept free of React and of the DOM on purpose: the interesting rules are
 * testable as plain data, and the component that draws them stays dumb.
 */
import type { Counts, Entry, Group, ProfileResult } from './protocol.js'

/** Tone is what the headline claims, and it is never inferred from a count alone. */
export type Tone = 'blocked' | 'warn' | 'incomplete' | 'clear'

/** Fact is one number in the summary strip. Always taken from `counts`. */
export interface Fact {
  label: string
  value: number
}

/** CandidateView is the first fix the solver proved, not a suggestion. */
export interface CandidateView {
  range: string
  note: string
  verified: boolean
}

/** GroupView is one root cause and the plugins it hits. */
export interface GroupView {
  severity: 'block' | 'warn'
  code: string
  reason?: string
  range?: string
  targets?: string[]
  members: string[]
  fix: string
  candidate?: CandidateView
  /** Shapes the solver generated and the engine refused, with the engine's reason. */
  refused: { range: string; why: string }[]
  refusal?: string
}

/** OtherView is a package that got no verdict. Listed, never folded into a count. */
export interface OtherView {
  name: string
  version?: string
  status: 'unknown' | 'not-a-plugin'
  reason?: string
}

/** MatrixView is everything the panel draws for one completed profile. */
export interface MatrixView {
  host: string
  tone: Tone
  headline: string
  detail: string
  facts: Fact[]
  groups: GroupView[]
  other: OtherView[]
  /** Problems that make the counts untrustworthy. Non-empty forces `incomplete`. */
  problems: string[]
}

/** PanelState is the three faces of the panel, plus the moment before the first call. */
export type PanelState =
  | { kind: 'idle' }
  | { kind: 'loading'; host: string }
  | { kind: 'error'; code: string; message: string }
  | { kind: 'result'; result: ProfileResult }

/** StateView is the non-result faces, reduced to what a reader needs. */
export interface StateView {
  tone: 'idle' | 'loading' | 'error' | Tone
  headline: string
  detail: string
  /** Set for errors; the panel shows the code verbatim so a bug report is searchable. */
  code?: string
}

/**
 * plural renders a count with its noun, because "1 plugins" reads like a bug and
 * a reader who spots one typo trusts the numbers less.
 */
function plural(n: number, noun: string): string {
  return `${n} ${noun}${n === 1 ? '' : 's'}`
}

/**
 * verb picks the agreement for a plural count, so "1 plugin loads" does not come
 * out as "1 plugin load". A reader who finds one broken sentence discounts every
 * number next to it.
 */
function verb(n: number): string {
  return n === 1 ? 'loads' : 'load'
}

/**
 * union returns the distinct plugin names across the groups of one severity.
 *
 * Distinct, because one plugin blocked by two causes appears in two groups and
 * must still count once when checked against `counts.blocked`.
 */
function union(groups: GroupView[], severity: 'block' | 'warn'): Set<string> {
  const names = new Set<string>()
  for (const g of groups) {
    if (g.severity !== severity) continue
    for (const name of g.members) names.add(name)
  }
  return names
}

/**
 * integrity reports every way the response contradicts itself.
 *
 * These are not cosmetic checks. Each one names a case where a number on screen
 * would describe a different set of packages than the list beside it, and the
 * answer to that is to say the profile is unusable rather than to pick one.
 */
function integrity(result: ProfileResult, view: { groups: GroupView[]; other: OtherView[] }): string[] {
  const { counts } = result
  const problems: string[] = []

  const judged = counts.ok + counts.warn + counts.blocked
  if (counts.plugins !== judged) {
    problems.push(`plugins=${counts.plugins} but ok+warn+blocked=${judged}`)
  }
  const accounted = counts.plugins + counts.unknown + counts.not_a_plugin
  if (counts.packages !== accounted) {
    problems.push(`packages=${counts.packages} but plugins+unknown+not_a_plugin=${accounted}`)
  }

  const blockedCovered = union(view.groups, 'block').size
  if (blockedCovered !== counts.blocked) {
    problems.push(`blocked=${counts.blocked} but the groups name ${blockedCovered}`)
  }
  const warnedCovered = union(view.groups, 'warn').size
  if (warnedCovered !== counts.warn) {
    problems.push(`warn=${counts.warn} but the groups name ${warnedCovered}`)
  }

  const unknownListed = view.other.filter((o) => o.status === 'unknown').length
  if (unknownListed !== counts.unknown) {
    problems.push(`unknown=${counts.unknown} but ${unknownListed} are listed`)
  }
  const notAPluginListed = view.other.filter((o) => o.status === 'not-a-plugin').length
  if (notAPluginListed !== counts.not_a_plugin) {
    problems.push(`not_a_plugin=${counts.not_a_plugin} but ${notAPluginListed} are listed`)
  }

  return problems
}

/**
 * factsFrom turns counts into the summary strip.
 *
 * `unknown` and `not_a_plugin` are shown only when non-zero: a permanent "0
 * unknown" trains a reader to stop seeing that row, which is the row that
 * matters when a profile is incomplete.
 */
function factsFrom(counts: Counts): Fact[] {
  const facts: Fact[] = [
    { label: 'packages', value: counts.packages },
    { label: 'plugins', value: counts.plugins },
    { label: 'ok', value: counts.ok },
    { label: 'warn', value: counts.warn },
    { label: 'blocked', value: counts.blocked },
  ]
  if (counts.unknown > 0) facts.push({ label: 'unknown', value: counts.unknown })
  if (counts.not_a_plugin > 0) facts.push({ label: 'not a plugin', value: counts.not_a_plugin })
  return facts
}

/** toOther keeps the packages that received no verdict, in listing order. */
function toOther(entries: Entry[]): OtherView[] {
  const out: OtherView[] = []
  for (const e of entries) {
    if (e.status !== 'unknown' && e.status !== 'not-a-plugin') continue
    out.push({ name: e.name, version: e.version, status: e.status, reason: e.reason })
  }
  return out
}

/** toGroup maps one Go group; the first candidate is the one worth showing. */
function toGroup(g: Group): GroupView {
  const first = g.fix_candidates?.[0]
  const view: GroupView = {
    severity: g.severity,
    code: g.code,
    members: g.plugins,
    fix: g.fix,
    refused: (g.fix_tried ?? []).map((t) => ({ range: t.range, why: t.why })),
  }
  if (g.reason) view.reason = g.reason
  if (g.range) view.range = g.range
  if (g.targets) view.targets = g.targets
  if (first) view.candidate = { range: first.range, note: first.note, verified: first.verified }
  if (g.fix_refusal) view.refusal = g.fix_refusal
  return view
}

/**
 * buildMatrix turns one profile into the panel's three blocks.
 *
 * Order is a product decision: what breaks, then what warns, then what could not
 * be judged. A reader who stops after the first line must have read the worst
 * true thing available.
 */
export function buildMatrix(result: ProfileResult): MatrixView {
  const groups = result.groups.map(toGroup)
  const other = toOther(result.entries)
  const problems = integrity(result, { groups, other })

  const view: MatrixView = {
    host: result.host,
    tone: 'clear',
    headline: '',
    detail: '',
    facts: factsFrom(result.counts),
    groups,
    other,
    problems,
  }

  const { counts } = result
  const host = result.host

  // A self-inconsistent response gets no verdict at all. Saying "3 blocked" from
  // counts while the list shows two is a coin flip the user pays for.
  if (problems.length > 0) {
    view.tone = 'incomplete'
    view.headline = `dshfit's answer about ${host} contradicts itself`
    view.detail =
      'The counts and the lists they describe do not agree, so this profile is not shown as a verdict: ' +
      problems.join('; ')
    return view
  }

  if (counts.blocked > 0) {
    view.tone = 'blocked'
    view.headline = `${counts.blocked} of ${plural(counts.plugins, 'plugin')} will not load on ${host}`
    view.detail =
      'Each group below is one root cause. The rewrite is the smallest edit that admits this host, ' +
      're-checked by the same engine — it is not a promise that the plugin works.'
    return view
  }

  if (counts.warn > 0) {
    view.tone = 'warn'
    view.headline = `${plural(counts.warn, 'plugin')} ${verb(counts.warn)} with a warning on ${host}`
    view.detail = 'Nothing is blocked, but this is not a clean profile: read the groups before upgrading.'
    return view
  }

  // Unknown is the case this panel exists to keep honest. Zero blocked with an
  // unread package is not "all clear" — it is "no answer for part of the list".
  if (counts.unknown > 0) {
    view.tone = 'incomplete'
    view.headline = `${plural(counts.unknown, 'package')} could not be judged on ${host}`
    view.detail =
      'Nothing that dshfit read is blocked, and that is all it can say. An unread package is not a ' +
      'compatible one: treat it as unknown until the reason below is fixed.'
    return view
  }

  if (counts.plugins === 0) {
    view.tone = 'incomplete'
    view.headline = `No plugins found on ${host}`
    view.detail = 'The listing held no package declaring dsh.bundle.patch, so there is nothing to clear.'
    return view
  }

  view.tone = 'clear'
  view.headline = `${plural(counts.plugins, 'plugin')} checked, none blocked on ${host}`
  view.detail = `${counts.ok} judged ok, nothing blocked, nothing unknown.`
  return view
}

/**
 * describeState reduces the three faces to copy.
 *
 * Errors carry their code through untouched. A panel that swallowed the code
 * would leave a user with a sentence and no way to search for it.
 */
export function describeState(state: PanelState): StateView {
  switch (state.kind) {
    case 'idle':
      return {
        tone: 'idle',
        headline: 'Not checked yet',
        detail: 'Run a profile to see which plugins will stop loading.',
      }
    case 'loading':
      return {
        tone: 'loading',
        headline: `Checking against ${state.host}…`,
        detail: 'Numbers appear when the whole listing has been judged; a partial count would be a guess.',
      }
    case 'error':
      return {
        tone: 'error',
        headline: `Could not check: ${state.code}`,
        detail: state.message,
        code: state.code,
      }
    case 'result':
      return buildMatrix(state.result)
  }
}
