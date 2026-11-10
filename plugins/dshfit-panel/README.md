# dshfit-panel

The DSH plugin half of dshfit: it owns one `dshfit serve` sidecar and exposes
`ctx.dshfit.profile()`, `.diff()` and `.version()` to the harness.

**What exists today:** the process lifecycle, the line protocol client (timeouts,
cancellation, error bubbling), the service surface, and the sidebar view — a
`sidebar.footer.action` seat that renders the matrix through a view model.
`test/sidecar.smoke.mjs` exercises the process half against the real Go binary and
a real Cordis `Context`; `test/render.test.mjs` and `test/client.test.mjs` pin the
sentences; `test/fixtures.test.mjs` compares the panel's numbers with
`dshfit profile --json` on the repo's real fixtures, field by field.

**What does not exist yet, stated so nobody has to discover it:** the browser half
has no route to the host. The profile is computed by a process on the host, the
panel runs in a page, and this plugin does not register the Remote endpoint that
would join them. The loader is therefore injected, and the default one fails with
`no-transport` instead of returning an empty profile — an empty profile renders as
"0 blocked", which is the one lie this project cannot afford. Wiring the host RPC
is the next step; until then, a panel with a real loader is the honest boundary.

**Why not `@deepseek-ai/dsh-client-ui-primitives`:** it is installable
(`0.2.2-rc.1`, peer `cordis ~4.1.0`) but it is React plus CSS-module assets —
`lib/index.js` opens with `import css from "./StateDot.module.css"`, which Node's
ESM loader cannot resolve, so nothing in it can be exercised by `node --test`
without the client's bundler. The panel therefore draws with plain elements and
`--dsw-*`-less class names, and the primitives stay available once there is a
bundler in the loop.

## What the panel refuses to say

- **Every number comes from `counts`.** Nothing in the render layer counts
  `entries`. `integrity()` treats a response whose counts disagree with the lists
  they describe as a refusal to answer, not as a cosmetic glitch: a summary saying
  three blocked above a list of two leaves a reader with no third number to trust.
- **Unknown is not clear.** Zero blocked with an unread package renders as
  `incomplete`, never as "none blocked". The clear verdict needs every package
  judged, nothing blocked, nothing warned, nothing unknown.
- **Not-a-plugin is listed, never grouped.** A package without
  `dsh.bundle.patch` has no verdict, so it has no root cause and cannot be counted
  as blocked.
- **The three faces stay apart.** `loading` shows no numbers (a partial count is a
  guess), `error` shows the stable code verbatim (a panel that swallowed it would
  leave a user with a sentence they cannot search for), and only `result` shows a
  matrix.

## Build

```console
$ npm ci
$ npm run build      # src/ -> lib/
$ npm test           # typecheck + smoke tests (needs Go and npm run build)
```

`lib/` is not committed. It is compiled output of `src/`, and a committed copy is
stale the first time someone edits a source file without rebuilding.

## Configuration

| key | meaning |
|---|---|
| `binPath` | absolute path to the `dshfit` binary; a bare name is resolved through `PATH` |
| `timeoutMs` | per-request deadline, default 10000 |
| `cwd` | working directory for the child; relative listing paths resolve against it |
| `host` | (client half) host version the panel judges against |
| `listing` | (client half) listing path or inline listing text |
| `load` | (client half) bridge to the sidecar; without one the panel reports `no-transport` |

A wrong `binPath` fails the first call with the spawn error. It never degrades
into an empty profile: "could not ask" and "nothing is wrong" are the two answers
this project exists to keep apart.

## Where the seat comes from

The seat name and the descriptor fields are the client's, not this project's. They
were read out of the shipped `@deepseek-ai/dsh-client-ui-cordis`, whose
`lib/client.js` registers its own panel with

```js
ctx.slots.inject('sidebar.footer.action', () => ctx.slots.register({
  name: 'sidebar.footer.action', id: 'cordis-panel', locale: NS,
  inject: () => ({ hooks: {…}, onRun, onStop })
}, CordisPanel))
```

and whose `package.json` declares the browser half as an `"./client"` export plus
`"dsh": { "client": { "inject": […], "platform": "web" } }`. `src/client.ts` follows
both. The `slots` service is typed locally rather than imported: the published types
are generic over a `SlotMap` that each UI package merges into, and pulling the
renderer tree in to name one seat would drag React, shiki and the markdown stack
into a plugin that needs none of them. `apply()` guards at runtime instead — a
Context without a slots service gets a line on stderr, not a silent no-op.

## Why the state is not in `#private` fields

Cordis wraps every service in a tracing proxy and calls getters and methods with
a *shadow* object — same prototype, not the instance the constructor ran on.
That shadow carries no private brand, so `#sidecar` throws
`Cannot read private member ... from an object whose class did not declare it`
the moment the panel calls in. Ordinary properties are read off the target and
work, which is why `DshfitPanel` uses them.

## Type mirroring

`src/protocol.ts` is copied field by field from the Go structs' `json` tags.
There is no compile-time link between the two languages: a field named
`notAPlugin` against `not_a_plugin` builds fine, renders `undefined`, and shows a
clean profile where there are blocked plugins. Every `omitempty` Go field is
optional here, because an omitted key is not `null`.
