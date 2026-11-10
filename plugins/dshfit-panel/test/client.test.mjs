// Markup tests for the panel, rendered through the real component.
//
// `renderToStaticMarkup` rather than a hand-built string: the thing under test is
// the component the client will mount, so a change that only looks right in a
// helper would not survive here. No DOM and no effects are involved — the state is
// handed in, which is exactly how the three faces differ.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { renderToStaticMarkup } from 'react-dom/server'

import { PanelFace, SEAT, unavailableLoader } from '../lib/client.js'

/** counts builds a self-consistent count block. */
function counts(over = {}) {
  const c = { packages: 0, plugins: 0, ok: 0, warn: 0, blocked: 0, not_a_plugin: 0, unknown: 0, ...over }
  c.plugins = over.plugins ?? c.ok + c.warn + c.blocked
  c.packages = over.packages ?? c.plugins + c.unknown + c.not_a_plugin
  return c
}

const BLOCKED = {
  host: '0.2.2-rc.1',
  projects: ['web'],
  groups: [
    {
      severity: 'block',
      code: 'peer-range-excludes-host',
      reason: 'prerelease-gate',
      range: '^0.2.0-rc.1',
      plugins: ['dsh-pet'],
      fix: 'name the new core: `|| >=0.2.2-rc.1`',
      fix_candidates: [
        { range: '^0.2.0-rc.1 || >=0.2.2-rc.1', shape: 'union-host', note: 'admits 0.2.2-rc.1', verified: true },
      ],
      fix_tried: [{ range: '|| >=0.2.2', shape: 'union-tagless', why: 'tagless bound excludes every prerelease' }],
    },
  ],
  entries: [{ name: 'dsh-gone', version: '0.1.0', status: 'unknown', reason: 'manifest unreadable' }],
  counts: counts({ ok: 1, blocked: 1, unknown: 1 }),
}

test('a result renders the counts, the group, its rewrite and the refused shape', () => {
  const html = renderToStaticMarkup(PanelFace({ state: { kind: 'result', result: BLOCKED } }))

  assert.match(html, /data-fact="blocked"><dt>blocked<\/dt><dd>1<\/dd>/, 'the strip prints counts.blocked')
  assert.match(html, /data-fact="packages"><dt>packages<\/dt><dd>3<\/dd>/)
  assert.match(html, /1 of 2 plugins will not load on 0\.2\.2-rc\.1/)
  assert.match(html, /peer-range-excludes-host/)
  assert.match(html, /\^0\.2\.0-rc\.1 \|\| &gt;=0\.2\.2-rc\.1/, 'the first proven rewrite is shown')
  assert.match(html, /tagless bound excludes every prerelease/, 'the refused shape stays visible')
  assert.match(html, /data-status="unknown"/, 'the unread package is listed, not folded away')
})

test('an unread package is never rendered as a clean profile', () => {
  const partial = {
    host: '0.2.2-rc.1',
    projects: ['web'],
    groups: [],
    entries: [{ name: 'dsh-gone', status: 'unknown', reason: 'manifest unreadable' }],
    counts: counts({ ok: 2, unknown: 1 }),
  }
  const html = renderToStaticMarkup(PanelFace({ state: { kind: 'result', result: partial } }))

  assert.doesNotMatch(html, /none blocked/)
  assert.match(html, /could not be judged/)
  assert.match(html, /data-tone="incomplete"/, 'the tone is incomplete, not ok')
})

test('the three faces are visibly different, and only the result shows numbers', () => {
  const loading = renderToStaticMarkup(PanelFace({ state: { kind: 'loading', host: '0.2.2-rc.1' } }))
  const error = renderToStaticMarkup(
    PanelFace({ state: { kind: 'error', code: 'no-transport', message: 'no route to the sidecar' } }),
  )

  assert.match(loading, /data-state="loading"/)
  assert.match(loading, /Checking against 0\.2\.2-rc\.1/)
  assert.doesNotMatch(loading, /data-fact=/, 'a partial count would be a guess')

  assert.match(error, /data-state="error"/)
  assert.match(error, /no-transport/, 'the code is on screen verbatim')
  assert.match(error, /no route to the sidecar/)
  assert.doesNotMatch(error, /data-fact=/, 'a failed run has no numbers to show')

  assert.notEqual(loading, error)
})

test('the default loader fails with a code instead of inventing an empty profile', async () => {
  await assert.rejects(
    () => unavailableLoader('0.2.2-rc.1', 'list.json'),
    (err) => err.code === 'no-transport' && /no Remote endpoint/.test(err.message),
  )
  assert.equal(SEAT, 'sidebar.footer.action', 'the seat name is the client\'s, not ours')
})
