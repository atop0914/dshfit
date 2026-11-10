// Unit tests for the view model: the rules that decide what a reader is told.
//
// These run without React and without a DOM on purpose. The sentences and the
// tone are the product decision; the component that draws them should have
// nothing left to decide, and if these tests were written against markup, a
// styling change could quietly move a verdict.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { buildMatrix, describeState } from '../lib/render.js'

/** counts builds a self-consistent count block; each test perturbs one field. */
function counts(over = {}) {
  const c = {
    packages: 0,
    plugins: 0,
    ok: 0,
    warn: 0,
    blocked: 0,
    not_a_plugin: 0,
    unknown: 0,
    ...over,
  }
  c.plugins = over.plugins ?? c.ok + c.warn + c.blocked
  c.packages = over.packages ?? c.plugins + c.unknown + c.not_a_plugin
  return c
}

/** profile builds a result whose counts describe exactly the lists given. */
function profile({ groups = [], entries = [], over = {} } = {}) {
  return { host: '0.2.2-rc.1', projects: ['app'], entries, groups, counts: counts(over) }
}

const BLOCK_GROUP = {
  severity: 'block',
  code: 'peer-range-excludes-host',
  reason: 'prerelease-gate',
  range: '^0.2.0-rc.1',
  plugins: ['dsh-pet'],
  fix: 'name the new core: `|| >=0.2.2-rc.1`',
  fix_candidates: [
    { range: '^0.2.0-rc.1 || >=0.2.2-rc.1', shape: 'union-host', note: 'admits 0.2.2-rc.1, keeps ^0.2.0-rc.1', verified: true },
    { range: '>=0.2.2-rc.1', shape: 'host-only', note: 'drops the original caret', verified: true },
  ],
  fix_tried: [{ range: '|| >=0.2.2', shape: 'union-tagless', why: 'tagless bound excludes every prerelease' }],
}

test('a blocked profile names the count, the host, and the first proven rewrite', () => {
  const m = buildMatrix(profile({ groups: [BLOCK_GROUP], over: { ok: 1, blocked: 1 } }))

  assert.equal(m.tone, 'blocked')
  assert.equal(m.headline, '1 of 2 plugins will not load on 0.2.2-rc.1')
  assert.equal(m.problems.length, 0)

  const blocked = m.facts.find((f) => f.label === 'blocked')
  assert.equal(blocked.value, 1, 'the strip must print counts.blocked')

  assert.equal(m.groups.length, 1)
  assert.equal(m.groups[0].candidate.range, '^0.2.0-rc.1 || >=0.2.2-rc.1', 'first candidate is the one shown')
  assert.equal(m.groups[0].candidate.verified, true)
  assert.equal(m.groups[0].refused.length, 1, 'the refused shape is kept: it is the shape authors write')
})

test('unknown is not clear: zero blocked with an unread package says so', () => {
  const m = buildMatrix(
    profile({
      entries: [{ name: 'dsh-gone', status: 'unknown', reason: 'manifest unreadable' }],
      over: { ok: 2, unknown: 1 },
    }),
  )

  assert.equal(m.tone, 'incomplete')
  assert.match(m.headline, /could not be judged/)
  assert.doesNotMatch(m.headline + ' ' + m.detail, /all clear/i)
  assert.doesNotMatch(m.headline + ' ' + m.detail, /none blocked/, 'nothing was proven, so nothing may claim it')
  assert.equal(m.other.length, 1)
  assert.equal(m.other[0].status, 'unknown')
})

test('not-a-plugin is listed apart and never counted as blocked', () => {
  const m = buildMatrix(
    profile({
      entries: [{ name: 'left-pad', status: 'not-a-plugin', reason: 'no dsh.bundle.patch' }],
      over: { ok: 1, not_a_plugin: 1 },
    }),
  )

  assert.equal(m.tone, 'clear')
  assert.equal(m.headline, '1 plugin checked, none blocked on 0.2.2-rc.1')
  assert.equal(m.facts.find((f) => f.label === 'blocked').value, 0)
  assert.equal(m.groups.length, 0, 'a non-plugin has no verdict, so it has no group')
  assert.deepEqual(m.other.map((o) => o.name), ['left-pad'])
})

test('a warning-only profile is not a clear one', () => {
  const m = buildMatrix(
    profile({
      groups: [{ ...BLOCK_GROUP, severity: 'warn', code: 'patch-entry-missing', fix: 'add the patch entry' }],
      over: { warn: 1 },
    }),
  )

  assert.equal(m.tone, 'warn')
  assert.match(m.headline, /^1 plugin loads with a warning on 0\.2\.2-rc\.1$/)
  assert.doesNotMatch(m.headline, /none blocked/)
})

test('counts that disagree with the lists produce a refusal, not a verdict', () => {
  // The shape a renderer that re-counted entries would produce: the strip says
  // four packages, the lists describe one. Printing "blocked=0" here would be a
  // green screen built on an inconsistency.
  const m = buildMatrix(profile({ groups: [BLOCK_GROUP], over: { ok: 1, blocked: 1, packages: 4 } }))

  assert.equal(m.tone, 'incomplete')
  assert.match(m.headline, /contradicts itself/)
  assert.ok(m.problems.length > 0)
  assert.match(m.problems.join(' '), /packages=4/)
})

test('a listing with no plugin at all is not a clean profile either', () => {
  const m = buildMatrix(
    profile({ entries: [{ name: 'left-pad', status: 'not-a-plugin' }], over: { not_a_plugin: 1 } }),
  )

  assert.equal(m.tone, 'incomplete')
  assert.match(m.headline, /No plugins/)
})

test('the unknown and not-a-plugin rows appear only when they are non-zero', () => {
  const quiet = buildMatrix(profile({ over: { ok: 1 } }))
  assert.deepEqual(
    quiet.facts.map((f) => f.label),
    ['packages', 'plugins', 'ok', 'warn', 'blocked'],
    'a permanent "0 unknown" row teaches people to stop reading that row',
  )

  const loud = buildMatrix(
    profile({ entries: [{ name: 'x', status: 'unknown' }], over: { ok: 1, unknown: 1 } }),
  )
  assert.ok(loud.facts.some((f) => f.label === 'unknown'))
})

test('the three faces never share a shape', () => {
  const idle = describeState({ kind: 'idle' })
  const loading = describeState({ kind: 'loading', host: '0.2.2-rc.1' })
  const error = describeState({ kind: 'error', code: 'no-transport', message: 'no route to the sidecar' })

  assert.equal(idle.tone, 'idle')
  assert.equal(loading.tone, 'loading')
  assert.equal(error.tone, 'error')
  assert.equal(error.code, 'no-transport', 'the code is shown verbatim so a report is searchable')
  assert.match(error.headline, /no-transport/)
  assert.match(error.detail, /no route/)

  const tones = new Set([idle.tone, loading.tone, error.tone])
  assert.equal(tones.size, 3, 'loading, error and idle must not look like each other')
})
