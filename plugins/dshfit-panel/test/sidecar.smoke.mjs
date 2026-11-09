// End-to-end checks for the TS half, run against the real Go binary.
//
// These are not unit tests of a mock: the sidecar under test is the actual
// `dshfit serve` process, and the plugin is mounted on a real Cordis Context.
// That is the only way to catch the failures this file is about — a promise that
// never settles, a response paired with the wrong request, a child that outlives
// its plugin.
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

import { Context } from '@deepseek-ai/cordis'

import { DshfitPanel } from '../lib/index.js'
import { Sidecar, TimeoutError } from '../lib/sidecar.js'
import { SidecarError } from '../lib/protocol.js'

const here = fileURLToPath(new URL('.', import.meta.url))
const repo = resolve(here, '..', '..', '..')
const listing = join(repo, 'testdata', 'profile', 'drift', 'list.json')
const HOST = '0.2.2-rc.1'

/** buildGoBinary compiles the CLI once per run; a stale binary would test the past. */
function buildGoBinary() {
  if (process.env.DSHFIT_BIN) return process.env.DSHFIT_BIN
  const out = join(mkdtempSync(join(tmpdir(), 'dshfit-smoke-')), process.platform === 'win32' ? 'dshfit.exe' : 'dshfit')
  execFileSync('go', ['build', '-o', out, './cmd/dshfit'], { cwd: repo, stdio: 'inherit' })
  return out
}

test('sidecar answers version, profile and diff over one process', async () => {
  const sidecar = new Sidecar({ command: buildGoBinary() })
  try {
    const v = await sidecar.version()
    assert.equal(v.name, 'dshfit')
    assert.equal(v.protocol, 1)

    const res = await sidecar.profile(HOST, listing)
    assert.equal(res.host, HOST)
    assert.equal(res.counts.packages, res.entries.length)
    // The drift fixture has two plugins that cannot load on this host. A zero
    // here would mean the result shape was misread, not that the fixture is clean.
    assert.equal(res.counts.blocked, 2)

    const d = await sidecar.diff('0.2.1-rc.3', HOST, listing)
    assert.equal(d.from, '0.2.1-rc.3')
    assert.equal(d.counts.worse, 1)
    assert.equal(d.counts.better, 1)
  } finally {
    sidecar.close()
  }
})

test('a sidecar error is a rejection carrying its code, and the session survives', async () => {
  const sidecar = new Sidecar({ command: buildGoBinary() })
  try {
    await assert.rejects(
      () => sidecar.profile(HOST, '-'),
      (err) => {
        assert.ok(err instanceof SidecarError)
        assert.equal(err.code, 'invalid-input')
        return true
      },
    )
    // Same process, still serving. A client that gave up here would restart the
    // sidecar for every rejected request.
    const v = await sidecar.version()
    assert.equal(v.protocol, 1)
  } finally {
    sidecar.close()
  }
})

test('an unknown method is an error, not an empty result', async () => {
  const sidecar = new Sidecar({ command: buildGoBinary() })
  try {
    await assert.rejects(
      () => sidecar.profile('banana', listing),
      (err) => {
        assert.ok(err instanceof SidecarError)
        assert.equal(err.code, 'invalid-input')
        return true
      },
    )
  } finally {
    sidecar.close()
  }
})

test('a child that never answers times out instead of hanging', async () => {
  // A stdin-consuming process that never writes a line: the panel's worst case,
  // and the one a `await` alone would wait on forever.
  const silent = new Sidecar({
    command: process.execPath,
    args: ['-e', 'process.stdin.resume()'],
    timeoutMs: 400,
  })
  try {
    await assert.rejects(() => silent.version(), TimeoutError)
  } finally {
    silent.close()
  }
})

test('pending requests reject when the child goes away', async () => {
  const sidecar = new Sidecar({ command: buildGoBinary(), timeoutMs: 5000 })
  const pending = sidecar.version()
  // Closing under a request is what a plugin unload looks like from the client
  // side. A silently dropped promise here becomes a spinner that never stops.
  sidecar.close()
  await assert.rejects(() => pending)
})

test('the plugin mounts on a real Context, answers, and lets go of its child', async () => {
  const root = new Context()
  await root.plugin(DshfitPanel, { binPath: buildGoBinary() })

  const panel = root.dshfit
  assert.equal(panel.started, false, 'starting a process at mount time costs a panel that is never opened')
  const res = await panel.profile(HOST, listing)
  assert.equal(res.counts.blocked, 2)
  assert.equal(panel.started, true)

  await root.fiber.dispose()
  // Disposal unregisters the service from the context: a panel that kept calling
  // ctx.dshfit after unload would get a live handle to a dead plugin.
  assert.equal(root.dshfit, undefined, 'the service must leave the context when its fiber is disposed')
  // And the instance that still exists refuses to start a new child, rather than
  // spawning one that nothing will ever close.
  assert.throws(() => panel.version(), /disposed/)
})
