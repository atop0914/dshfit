/**
 * A client for `dshfit serve`: one child process, one request per line, one
 * response per line.
 *
 * The two failure modes this file exists to prevent are both silent in a panel:
 *
 * - A promise that never settles. If the child dies, every pending request must
 *   be rejected — otherwise the panel shows a spinner forever and the user reads
 *   it as "still checking".
 * - A response paired with the wrong request. Requests are answered in any
 *   order, so the id is the only pairing key; the map below is the pairing.
 *
 * Everything a child cannot answer is a rejection, never a default value. A
 * timeout that resolved to an empty profile would be indistinguishable from a
 * clean one, which is the one answer this whole tool must never give.
 */
import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process'

import {
  SidecarError,
  type DiffResult,
  type ErrorCode,
  type ProfileResult,
  type VersionInfo,
} from './protocol.js'

/** Default per-request deadline. A profile of a large listing is seconds, not minutes. */
export const DEFAULT_TIMEOUT_MS = 10_000

/** A response line larger than this is a bug in the sidecar, not a big listing. */
const MAX_LINE_BYTES = 32 << 20

/** How much stderr to keep for diagnostics. Enough for a stack, not a log file. */
const STDERR_KEEP = 8 << 10

/** SidecarOptions says how to start the child. */
export interface SidecarOptions {
  /** Absolute path to the dshfit binary. */
  command: string
  /** Extra arguments, normally none: `serve` takes no flags. */
  args?: string[]
  /** Per-request deadline in milliseconds. */
  timeoutMs?: number
  /** Working directory for the child, used to resolve a relative listing path. */
  cwd?: string
}

/** TimeoutError is a request the child did not answer in time. */
export class TimeoutError extends Error {
  constructor(method: string, ms: number) {
    super(`dshfit serve did not answer ${method} within ${ms}ms`)
    this.name = 'TimeoutError'
  }
}

/** SidecarExitedError is a child that went away with requests still pending. */
export class SidecarExitedError extends Error {
  constructor(reason: string, stderr: string) {
    super(stderr ? `${reason}; stderr: ${stderr}` : reason)
    this.name = 'SidecarExitedError'
  }
}

interface Pending {
  method: string
  resolve: (value: unknown) => void
  reject: (err: Error) => void
  timer: NodeJS.Timeout
}

interface WireResponse {
  id: number | string | null
  result?: unknown
  error?: { code: ErrorCode; message: string }
}

export class Sidecar {
  readonly #child: ChildProcessWithoutNullStreams
  readonly #timeoutMs: number
  readonly #pending = new Map<number, Pending>()
  #nextId = 1
  #buffer = ''
  #stderr = ''
  #closed: Error | null = null

  constructor(options: SidecarOptions) {
    this.#timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS
    this.#child = spawn(options.command, options.args ?? ['serve'], {
      cwd: options.cwd,
      stdio: ['pipe', 'pipe', 'pipe'],
      windowsHide: true,
    })

    this.#child.stdout.setEncoding('utf8')
    this.#child.stdout.on('data', (chunk: string) => this.#onData(chunk))

    this.#child.stderr.setEncoding('utf8')
    this.#child.stderr.on('data', (chunk: string) => {
      // stderr is diagnostics, never protocol. Keeping the tail means an error
      // message can quote what the child actually said instead of guessing.
      this.#stderr = (this.#stderr + chunk).slice(-STDERR_KEEP)
    })

    this.#child.on('error', (err) => this.#fail(new SidecarExitedError(`cannot start: ${err.message}`, '')))
    this.#child.on('exit', (code, signal) => {
      this.#fail(
        new SidecarExitedError(
          `dshfit serve exited (${signal ?? `code ${code ?? 'unknown'}`})`,
          this.#stderr.trim(),
        ),
      )
    })
  }

  /** version reports the sidecar's identity and protocol version. */
  version(): Promise<VersionInfo> {
    return this.#request<VersionInfo>('version', undefined)
  }

  /**
   * profile judges a listing against one host.
   *
   * `listing` is the listing text or a path to it; `base` is the directory
   * relative paths inside an inline listing resolve against, and is ignored for
   * a path because the path already answers that.
   */
  profile(host: string, listing: string, base?: string): Promise<ProfileResult> {
    return this.#request<ProfileResult>('profile', { host, listing, base })
  }

  /** diff reports what changes between two hosts, from one listing. */
  diff(from: string, to: string, listing: string, base?: string): Promise<DiffResult> {
    return this.#request<DiffResult>('diff', { from, to, listing, base })
  }

  /**
   * close stops the child and rejects anything still waiting.
   *
   * Callers get a rejection rather than a silent drop: a request that
   * disappears is a spinner that never stops.
   */
  close(): void {
    this.#fail(new SidecarExitedError('sidecar closed', ''))
    this.#child.stdin.end()
    this.#child.kill()
  }

  /** stderr is the diagnostic tail, useful when a call failed for an unclear reason. */
  get stderr(): string {
    return this.#stderr.trim()
  }

  #request<T>(method: string, params: unknown): Promise<T> {
    if (this.#closed) return Promise.reject(this.#closed)

    const id = this.#nextId++
    const line = JSON.stringify(params === undefined ? { id, method } : { id, method, params })

    return new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.#pending.delete(id)
        reject(new TimeoutError(method, this.#timeoutMs))
      }, this.#timeoutMs)

      this.#pending.set(id, { method, resolve: resolve as (value: unknown) => void, reject, timer })

      // A write to a dead child raises EPIPE here rather than on the promise;
      // without this the request would wait for its timer to notice.
      this.#child.stdin.write(`${line}\n`, (err) => {
        if (!err) return
        const waiter = this.#pending.get(id)
        if (!waiter) return
        this.#pending.delete(id)
        clearTimeout(waiter.timer)
        waiter.reject(new SidecarExitedError(`cannot write request: ${err.message}`, this.stderr))
      })
    })
  }

  #onData(chunk: string): void {
    this.#buffer += chunk
    if (this.#buffer.length > MAX_LINE_BYTES) {
      // Unlike the sidecar, this side cannot resynchronise: a client that has
      // lost the line boundary has no way to find it again.
      this.#fail(new SidecarExitedError('response line exceeded the client buffer', this.stderr))
      this.#buffer = ''
      return
    }
    for (;;) {
      const at = this.#buffer.indexOf('\n')
      if (at < 0) return
      const line = this.#buffer.slice(0, at).replace(/\r$/, '')
      this.#buffer = this.#buffer.slice(at + 1)
      if (line.trim() === '') continue
      this.#onLine(line)
    }
  }

  #onLine(line: string): void {
    let resp: WireResponse
    try {
      resp = JSON.parse(line) as WireResponse
    } catch (err) {
      // A line this side cannot parse means every pending request is now
      // unanswerable: their responses may have been the one that broke.
      this.#fail(new SidecarExitedError(`unparseable response line: ${String(err)}`, this.stderr))
      return
    }
    if (typeof resp.id !== 'number') {
      // The sidecar answers a request it could not attribute with a null id.
      // Nobody is waiting for it, so it can only be reported, not resolved.
      return
    }
    const waiter = this.#pending.get(resp.id)
    if (!waiter) return
    this.#pending.delete(resp.id)
    clearTimeout(waiter.timer)

    if (resp.error) {
      waiter.reject(new SidecarError(resp.error.code, resp.error.message))
      return
    }
    waiter.resolve(resp.result)
  }

  #fail(err: Error): void {
    if (this.#closed) return
    this.#closed = err
    for (const [id, waiter] of this.#pending) {
      this.#pending.delete(id)
      clearTimeout(waiter.timer)
      waiter.reject(err)
    }
  }
}
