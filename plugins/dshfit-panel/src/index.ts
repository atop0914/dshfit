/**
 * The DSH plugin half of dshfit.
 *
 * It owns one sidecar process and exposes the three answers as a service:
 * `ctx.dshfit.profile(...)`, `.diff(...)`, `.version()`. It renders nothing and
 * decides nothing — every verdict in the result was produced by the Go core,
 * which is the only judge in this project. A panel that recomputed "is it
 * blocked" from the raw entries would eventually disagree with `dshfit profile`
 * on the same file, and a user has no way to tell which answer is the product.
 *
 * Scope of what exists today: the process lifecycle, the request/response
 * plumbing with timeouts and cancellation, and the service surface. The sidebar
 * view that draws the matrix is the next step; until it lands, install this and
 * nothing appears on screen.
 */
import { Context, Service } from '@deepseek-ai/cordis'

import type { DiffResult, ProfileResult, VersionInfo } from './protocol.js'
import { Sidecar } from './sidecar.js'

declare module '@deepseek-ai/cordis' {
  interface Context {
    dshfit: DshfitPanel
  }
}

export const name = 'dshfit-panel'

/** Config is what the bundle patch or the harness config can set. */
export interface Config {
  /**
   * Absolute path to the dshfit binary. A bare name is resolved through PATH,
   * which is fine on a developer machine and not on a packaged install; when it
   * is wrong the first call fails with the spawn error rather than an empty
   * profile, so the mistake is visible instead of silent.
   */
  binPath?: string
  /** Per-request deadline in milliseconds. */
  timeoutMs?: number
  /** Working directory for the child: relative listing paths resolve against it. */
  cwd?: string
}

/** DefaultBinPath is what a terminal `go install` puts on PATH. */
export const DEFAULT_BIN = 'dshfit'

export class DshfitPanel extends Service {
  /**
   * State is held in ordinary properties, not `#private` fields.
   *
   * Cordis wraps every service in a tracing proxy and invokes getters and
   * methods with a *shadow* object — same prototype, not the instance that the
   * constructor ran on. That shadow carries no private brand, so `#sidecar`
   * throws `Cannot read private member ... from an object whose class did not
   * declare it` the first time a panel calls in. Plain properties are read off
   * the target and work; the reachability is the reason, not a style choice.
   */
  private readonly config: Config
  private sidecar: Sidecar | null = null
  /**
   * Requests in flight at close time are rejected by the sidecar, not awaited:
   * disposal is what happens when the plugin is unloaded, and a panel being torn
   * down has nobody left to show a late result to.
   */
  private disposed = false

  constructor(ctx: Context, config: Config = {}) {
    super(ctx, 'dshfit')
    this.config = config
    // The child is started on first use, not here: a panel that is mounted and
    // never opened should not cost a process. ctx.effect ties its lifetime to
    // this fiber, so unloading the plugin cannot leave an orphan sidecar behind
    // — the exact leak a manual `process.on('exit')` would leave.
    ctx.effect(() => () => {
      this.disposed = true
      this.sidecar?.close()
      this.sidecar = null
    })
  }

  /** started is true once a sidecar process exists. */
  get started(): boolean {
    return this.sidecar !== null
  }

  /** version reports the sidecar's identity and wire protocol version. */
  version(): Promise<VersionInfo> {
    return this.ensure().version()
  }

  /**
   * profile judges a listing against one host.
   *
   * `listing` is the listing text or a path to it. `base` is the directory
   * relative package paths inside an inline listing resolve against, and is
   * ignored for a path because the path already answers that question.
   */
  profile(host: string, listing: string, base?: string): Promise<ProfileResult> {
    return this.ensure().profile(host, listing, base)
  }

  /**
   * diff reports what changes between two hosts, from one listing.
   *
   * Both hosts are required on purpose: a default on either side would answer a
   * question about a version the user never named.
   */
  diff(from: string, to: string, listing: string, base?: string): Promise<DiffResult> {
    return this.ensure().diff(from, to, listing, base)
  }

  /** close stops the sidecar. The next call starts a new one. */
  close(): void {
    this.sidecar?.close()
    this.sidecar = null
  }

  private ensure(): Sidecar {
    if (this.disposed) {
      throw new Error('dshfit-panel is disposed; a disposed plugin cannot start a sidecar')
    }
    if (this.sidecar) return this.sidecar
    this.sidecar = new Sidecar({
      command: this.config.binPath || DEFAULT_BIN,
      timeoutMs: this.config.timeoutMs,
      cwd: this.config.cwd,
    })
    return this.sidecar
  }
}

export default DshfitPanel
