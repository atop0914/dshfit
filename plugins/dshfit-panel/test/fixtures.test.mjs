// The panel against the real engine: the same listing, judged by the Go core,
// rendered by the component, numbers compared field by field.
//
// A view-model test that only ever sees hand-written JSON proves the renderer is
// consistent with itself. This file is the one that can catch the failure the
// project actually fears — the panel and `dshfit profile` disagreeing about the
// same input, leaving a user with two answers and no way to choose.
import assert from 'node:assert/strict'
import { execFileSync, spawnSync } from 'node:child_process'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

import { renderToStaticMarkup } from 'react-dom/server'

import { PanelFace } from '../lib/client.js'
import { buildMatrix } from '../lib/render.js'

const here = fileURLToPath(new URL('.', import.meta.url))
const repo = resolve(here, '..', '..', '..')

/** buildGoBinary compiles once per run; a stale binary would test the past. */
function buildGoBinary() {
  if (process.env.DSHFIT_BIN) return process.env.DSHFIT_BIN
  const out = join(
    mkdtempSync(join(tmpdir(), 'dshfit-panel-')),
    process.platform === 'win32' ? 'dshfit.exe' : 'dshfit',
  )
  execFileSync('go', ['build', '-o', out, './cmd/dshfit'], { cwd: repo, stdio: 'inherit' })
  return out
}

/**
 * profileJson runs the real CLI and returns what it actually printed.
 *
 * A non-zero exit is a normal answer here, not a failure: `dshfit profile` signals
 * "something blocks" through its exit code. The run is still required to be a real
 * verdict — the JSON must parse, and an exit outside the four documented codes is
 * treated as a crash rather than as a result.
 */
function profileJson(bin, fixture, host) {
  const listing = join(repo, 'testdata', 'profile', fixture, 'list.json')
  const run = spawnSync(bin, ['profile', '--host', host, '--json', listing], {
    cwd: repo,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  })
  assert.ok(run.status !== null, `dshfit did not exit normally: ${run.signal}`)
  assert.ok(run.status <= 2, `dshfit exited ${run.status}, which is not one of its verdict codes: ${run.stderr}`)
  assert.ok(run.stdout.trim().length > 0, 'the CLI printed no answer')
  return JSON.parse(run.stdout)
}

/** FACT_KEYS maps the panel's labels onto the JSON keys they must agree with. */
const FACT_KEYS = {
  packages: 'packages',
  plugins: 'plugins',
  ok: 'ok',
  warn: 'warn',
  blocked: 'blocked',
  unknown: 'unknown',
  'not a plugin': 'not_a_plugin',
}

for (const [fixture, host] of [
  ['drift', '0.3.0'],
  ['web', '0.2.2-rc.1'],
]) {
  test(`${fixture} @ ${host}: every number the panel shows is the CLI's own`, () => {
    const bin = buildGoBinary()
    const result = profileJson(bin, fixture, host)
    const view = buildMatrix(result)
    const html = renderToStaticMarkup(PanelFace({ state: { kind: 'result', result } }))

    assert.equal(view.host, result.host)
    assert.ok(view.facts.length >= 5)

    for (const fact of view.facts) {
      const key = FACT_KEYS[fact.label]
      assert.ok(key, `unknown fact label ${fact.label}`)
      assert.equal(fact.value, result.counts[key], `${fact.label} must equal counts.${key}`)
      assert.match(
        html,
        new RegExp(`data-fact="${fact.label}"[^>]*><dt>${fact.label}</dt><dd>${fact.value}</dd>`),
        `${fact.label} is drawn from the same field it claims`,
      )
    }

    // The two rejection kinds stay apart in this fixture, so the panel must too.
    assert.ok(result.counts.blocked > 0, `${fixture} is meant to have blocked plugins`)
    assert.equal(view.tone, 'blocked')
    assert.equal(view.problems.length, 0, `a self-contradicting answer would be a bug: ${view.problems}`)
    assert.equal(view.groups.filter((g) => g.severity === 'block').length, result.groups.filter((g) => g.severity === 'block').length)

    for (const g of view.groups) {
      assert.ok(g.fix.length > 0, `group ${g.code} must carry a prescription`)
      if (g.severity === 'block' && g.code === 'peer-range-excludes-host') {
        assert.ok(g.candidate, `${fixture}: a range cause must offer a verified rewrite`)
        assert.equal(g.candidate.verified, true)
      }
    }

    // Every blocked plugin named by the CLI reaches the screen by name.
    for (const name of result.groups.flatMap((g) => g.plugins)) {
      assert.ok(html.includes(name), `${name} was judged but is not rendered`)
    }
  })
}
