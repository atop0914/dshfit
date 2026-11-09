/**
 * Wire types, copied field by field from the Go structs' json tags.
 *
 * There is no compile-time link between the two languages, and that is exactly
 * the hazard: a field named `notAPlugin` here against `not_a_plugin` there does
 * not fail to build, it renders `undefined`, and the panel shows zeroes as if
 * the profile were clean. Every Go field is optional here when it is
 * `omitempty` on that side — an omitted key is not a `null`, so anything reading
 * these values has to handle absence explicitly.
 */

/** Status is what dshfit was able to say about one package. */
export type Status = 'plugin' | 'not-a-plugin' | 'unknown'

/** Severity of one finding. */
export type Severity = 'block' | 'warn'

/** Verdict for one plugin against one host. */
export type Verdict = 'ok' | 'warn' | 'blocked'

/** ChangeKind says which way one package moved between two hosts. */
export type ChangeKind = 'worse' | 'better' | 'cause' | 'added' | 'removed'

/** Counts mirrors profile.Counts. */
export interface Counts {
  packages: number
  plugins: number
  ok: number
  warn: number
  blocked: number
  not_a_plugin: number
  unknown: number
}

/** Issue mirrors compat.Issue. */
export interface Issue {
  severity: Severity
  code: string
  package?: string
  range?: string
  /** out-of-range needs a wider range; prerelease-gate needs the new core named. */
  reason?: string
  detail: string
}

/** Report mirrors compat.Report. */
export interface Report {
  plugin: string
  version: string
  dir: string
  source?: 'dir' | 'tarball'
  host: string
  verdict: Verdict
  issues: Issue[]
}

/** FixCandidate mirrors fix.Candidate. */
export interface FixCandidate {
  range: string
  shape: string
  /** Says what the rewrite promises beyond admitting the host — never compatibility. */
  note: string
  /** The engine re-parsed this range and re-tested the host; nothing is assumed. */
  verified: boolean
}

/** FixAttempt mirrors fix.Attempt: a shape the solver generated and the engine refused. */
export interface FixAttempt {
  range: string
  shape: string
  why: string
}

/** Group mirrors profile.Group: one root cause and every plugin it hits. */
export interface Group {
  severity: Severity
  code: string
  reason?: string
  range?: string
  targets?: string[]
  plugins: string[]
  fix: string
  fix_candidates?: FixCandidate[]
  fix_tried?: FixAttempt[]
  fix_refusal?: string
}

/** Entry mirrors profile.Entry. */
export interface Entry {
  name: string
  version?: string
  dir?: string
  status: Status
  reason?: string
  report?: Report
}

/** ProfileResult mirrors profile.Result. */
export interface ProfileResult {
  host: string
  projects: string[]
  entries: Entry[]
  groups: Group[]
  counts: Counts
}

/** Side mirrors profile.Side. */
export interface Side {
  present: boolean
  status?: Status
  verdict?: Verdict
  version?: string
  /** e.g. "peer-range-excludes-host(prerelease-gate)", sorted. */
  causes?: string[]
  reason?: string
}

/** Change mirrors profile.Change. */
export interface Change {
  kind: ChangeKind
  name: string
  from: Side
  to: Side
}

/** DiffCounts mirrors profile.DiffCounts. */
export interface DiffCounts {
  worse: number
  better: number
  cause: number
  added: number
  removed: number
}

/** DiffResult mirrors profile.Diff. */
export interface DiffResult {
  from: string
  to: string
  projects?: string[]
  changes: Change[]
  counts: DiffCounts
  to_counts: Counts
}

/** VersionInfo mirrors rpc.versionInfo. */
export interface VersionInfo {
  name: string
  version: string
  protocol: number
}

/** Stable error codes the sidecar reports. */
export type ErrorCode = 'bad-request' | 'unknown-method' | 'invalid-input' | 'internal'

/** SidecarError is a failure the sidecar reported about one request. */
export class SidecarError extends Error {
  constructor(
    readonly code: ErrorCode,
    message: string,
  ) {
    super(message)
    this.name = 'SidecarError'
  }
}
