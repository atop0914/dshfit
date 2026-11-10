/**
 * The browser half of dshfit: one seat in the sidebar, three faces, no opinions.
 *
 * Where the existing entries sit in the DSH client is not inventable, so it was
 * read out of the shipped code rather than guessed. `@deepseek-ai/dsh-client-ui-cordis`
 * registers its panel with
 *
 *     ctx.slots.inject('sidebar.footer.action', () => ctx.slots.register({
 *       name: 'sidebar.footer.action', id: 'cordis-panel', locale: NS,
 *       inject: () => ({ hooks: {...}, onRun, onStop })
 *     }, CordisPanel))
 *
 * and declares the entry point in its own package.json as
 * `"dsh": { "client": { "inject": [...], "platform": "web" } }` plus an
 * `"./client"` export. This file follows both, so the seat name and the
 * descriptor fields are the client's, not ours.
 *
 * The gap, stated plainly because a quiet fallback would hide it: the panel runs
 * in the browser and the profile is computed by a process on the host, and this
 * plugin does not yet register a Remote endpoint to bridge them. So the loader is
 * injected, and the default one fails with `no-transport` rather than returning an
 * empty profile. An empty profile would render as "0 blocked" — a green screen
 * produced by a missing wire, which is the one lie this project cannot afford.
 */
import { createElement, useEffect, useState, type ReactElement, type ReactNode } from 'react'

import type { Context } from '@deepseek-ai/cordis'

import type { ProfileResult } from './protocol.js'
import { SidecarError } from './protocol.js'
import { buildMatrix, describeState, type MatrixView, type PanelState, type StateView, type Tone } from './render.js'

export const name = 'dshfit-panel'

/** The seat a sidebar footer action registers into, as named by the DSH client. */
export const SEAT = 'sidebar.footer.action'

/**
 * Loader answers one profile request. Injected because the transport is the
 * unsolved half: a host RPC registration belongs on the host side, and pretending
 * a browser can start a child process would be a fiction.
 */
export type Loader = (host: string, listing: string) => Promise<ProfileResult>

/** Config is what the bundle patch or the harness config can set. */
export interface Config {
  /** Host version to judge against. */
  host?: string
  /** Listing path or inline listing text, passed through to the Go core. */
  listing?: string
  /** Bridge to the Go sidecar. Without one the panel reports no-transport. */
  load?: Loader
}

/** InjectedProps is the face the seat hands the component. */
export interface InjectedProps {
  host: string
  listing: string
  load: Loader
}

/**
 * unavailableLoader is the honest default: it fails, with a code that says why.
 */
export const unavailableLoader: Loader = async () => {
  throw new SidecarError(
    'no-transport',
    'the browser half has no route to the dshfit sidecar yet (no Remote endpoint is registered), ' +
      'so this panel cannot produce a profile instead of producing an empty one',
  )
}

/**
 * slots is declared here because its published types are generic over a SlotMap
 * that each UI package merges into, and importing the whole renderer tree to name
 * one seat would pull React, shiki and the markdown stack into a plugin that
 * needs none of them. The shape below matches the calls the shipped panel makes;
 * the runtime guard in `apply` covers the case where a future client disagrees.
 */
interface SlotRegistry {
  inject(name: string, register: () => unknown): unknown
  register(descriptor: SlotDescriptor, component: (props: InjectedProps) => ReactElement): unknown
}

interface SlotDescriptor {
  name: string
  id: string
  inject: () => InjectedProps
}

declare module '@deepseek-ai/cordis' {
  interface Context {
    slots?: SlotRegistry
  }
}

/** TONE_CLASS keeps the visual weight in one table instead of scattered ternaries. */
const TONE_CLASS: Record<Tone | 'idle' | 'loading' | 'error', string> = {
  blocked: 'dshfit-bad',
  warn: 'dshfit-warn',
  incomplete: 'dshfit-unknown',
  clear: 'dshfit-ok',
  idle: 'dshfit-idle',
  loading: 'dshfit-loading',
  error: 'dshfit-bad',
}

/** Headline is the sentence a reader stops at. */
function Headline({ view }: { view: StateView }): ReactElement {
  return createElement(
    'div',
    { className: `dshfit-headline ${TONE_CLASS[view.tone]}`, 'data-tone': view.tone },
    createElement('strong', { className: 'dshfit-headline-text' }, view.headline),
    view.code ? createElement('code', { className: 'dshfit-code' }, view.code) : null,
    createElement('p', { className: 'dshfit-detail' }, view.detail),
  )
}

/** Facts renders the summary strip; every value came out of `counts`. */
function Facts({ facts }: { facts: MatrixView['facts'] }): ReactElement {
  return createElement(
    'dl',
    { className: 'dshfit-facts' },
    ...facts.map((f) =>
      createElement(
        'div',
        { className: 'dshfit-fact', 'data-fact': f.label, key: f.label },
        createElement('dt', null, f.label),
        createElement('dd', null, String(f.value)),
      ),
    ),
  )
}

/** GroupRow is one root cause: who it hits, what to change, what was refused. */
function GroupRow({ group }: { group: MatrixView['groups'][number] }): ReactElement {
  const children: ReactNode[] = [
    createElement(
      'div',
      { className: 'dshfit-group-head', key: 'head' },
      createElement('code', { className: 'dshfit-code' }, group.code),
      group.reason ? createElement('span', { className: 'dshfit-reason' }, group.reason) : null,
      group.range ? createElement('span', { className: 'dshfit-range' }, group.range) : null,
    ),
    createElement(
      'ul',
      { className: 'dshfit-members', key: 'members' },
      ...group.members.map((m) => createElement('li', { key: m }, m)),
    ),
    createElement('p', { className: 'dshfit-fix', key: 'fix' }, group.fix),
  ]

  if (group.candidate) {
    children.push(
      createElement(
        'p',
        { className: 'dshfit-candidate', key: 'candidate', 'data-verified': String(group.candidate.verified) },
        createElement('code', null, group.candidate.range),
        createElement('span', { className: 'dshfit-note' }, group.candidate.note),
      ),
    )
  }
  if (group.refusal) {
    children.push(createElement('p', { className: 'dshfit-refusal', key: 'refusal' }, group.refusal))
  }
  if (group.refused.length > 0) {
    children.push(
      createElement(
        'ul',
        { className: 'dshfit-refused', key: 'refused' },
        ...group.refused.map((r) =>
          createElement('li', { key: r.range }, createElement('code', null, r.range), ` — ${r.why}`),
        ),
      ),
    )
  }

  return createElement(
    'section',
    {
      className: `dshfit-group ${group.severity === 'block' ? 'dshfit-bad' : 'dshfit-warn'}`,
      'data-severity': group.severity,
      'data-code': group.code,
    },
    ...children,
  )
}

/** OtherRow is a package with no verdict, shown apart from the ones that have one. */
function OtherRow({ entry }: { entry: MatrixView['other'][number] }): ReactElement {
  return createElement(
    'li',
    { className: 'dshfit-other', 'data-status': entry.status },
    createElement('span', { className: 'dshfit-other-name' }, entry.name),
    entry.version ? createElement('span', { className: 'dshfit-other-version' }, entry.version) : null,
    createElement('span', { className: 'dshfit-other-status' }, entry.status),
    entry.reason ? createElement('span', { className: 'dshfit-other-reason' }, entry.reason) : null,
  )
}

/**
 * Matrix draws one finished profile.
 *
 * It renders the view model as given: no counting, no filtering of groups by a
 * rule of its own. Anything this component decided independently of `buildMatrix`
 * would be a second verdict.
 */
export function Matrix({ view }: { view: MatrixView }): ReactElement {
  const blocked = view.groups.filter((g) => g.severity === 'block')
  const warned = view.groups.filter((g) => g.severity === 'warn')

  return createElement(
    'div',
    { className: 'dshfit-matrix', 'data-tone': view.tone },
    createElement(Headline, {
      view: { tone: view.tone, headline: view.headline, detail: view.detail },
    }),
    createElement(Facts, { facts: view.facts }),
    blocked.length > 0
      ? createElement(
          'div',
          { className: 'dshfit-section', 'data-section': 'blocked' },
          createElement('h4', null, 'Will not load'),
          ...blocked.map((g, i) => createElement(GroupRow, { group: g, key: `${g.code}-${i}` })),
        )
      : null,
    warned.length > 0
      ? createElement(
          'div',
          { className: 'dshfit-section', 'data-section': 'warn' },
          createElement('h4', null, 'Loads with warnings'),
          ...warned.map((g, i) => createElement(GroupRow, { group: g, key: `${g.code}-${i}` })),
        )
      : null,
    view.other.length > 0
      ? createElement(
          'div',
          { className: 'dshfit-section', 'data-section': 'unjudged' },
          createElement('h4', null, 'No verdict'),
          createElement('ul', { className: 'dshfit-others' }, ...view.other.map((o) => createElement(OtherRow, { entry: o, key: o.name }))),
        )
      : null,
    view.problems.length > 0
      ? createElement(
          'ul',
          { className: 'dshfit-problems' },
          ...view.problems.map((p) => createElement('li', { key: p }, p)),
        )
      : null,
  )
}

/** PanelFace is the whole panel for one state, with no effects and no clock. */
export function PanelFace({ state }: { state: PanelState }): ReactElement {
  if (state.kind === 'result') return createElement(Matrix, { view: buildMatrix(state.result) })
  const view = describeState(state)
  return createElement(
    'div',
    { className: `dshfit-panel dshfit-${state.kind}`, 'data-state': state.kind },
    createElement(Headline, { view }),
  )
}

/**
 * Panel runs one profile on mount and whenever the inputs change.
 *
 * The stale-response guard is a live flag rather than an abort signal because the
 * loader is injected: the default one rejects immediately, and a real bridge may
 * not accept a signal. A panel rendering the answer to a question the user has
 * already changed is worse than a panel that renders nothing for a moment.
 */
export function Panel(props: InjectedProps): ReactElement {
  const [state, setState] = useState<PanelState>({ kind: 'idle' })

  useEffect(() => {
    let live = true
    setState({ kind: 'loading', host: props.host })
    props.load(props.host, props.listing).then(
      (result) => {
        if (live) setState({ kind: 'result', result })
      },
      (err: unknown) => {
        if (!live) return
        const code = err instanceof SidecarError ? err.code : 'internal'
        setState({ kind: 'error', code, message: err instanceof Error ? err.message : String(err) })
      },
    )
    return () => {
      live = false
    }
  }, [props.host, props.listing, props.load])

  return createElement(PanelFace, { state })
}

/**
 * apply registers the seat.
 *
 * A client without a slots service is reported on stderr and left alone: throwing
 * here would take down a Context that is otherwise fine, and silently returning
 * would leave a user staring at a panel that was never mounted with no way to
 * learn why.
 */
export function apply(ctx: Context, config: Config = {}): void {
  const slots = ctx.slots
  if (!slots) {
    process.stderr.write('dshfit-panel: this Context has no slots service; the sidebar seat was not registered\n')
    return
  }

  const host = config.host ?? ''
  const listing = config.listing ?? ''
  const load = config.load ?? unavailableLoader

  slots.inject(SEAT, () =>
    slots.register(
      {
        name: SEAT,
        id: 'dshfit-panel',
        inject: () => ({ host, listing, load }),
      },
      Panel,
    ),
  )
}

export default apply
