# dshfit-panel

The DSH plugin half of dshfit: it owns one `dshfit serve` sidecar and exposes
`ctx.dshfit.profile()`, `.diff()` and `.version()` to the harness.

**What exists today:** the process lifecycle, the line protocol client (timeouts,
cancellation, error bubbling), and the service surface, all exercised by
`test/sidecar.smoke.mjs` against the real Go binary and a real Cordis `Context`.
**What does not exist yet:** the sidebar view. Install this and nothing appears on
screen — rendering the matrix is the next step.

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

A wrong `binPath` fails the first call with the spawn error. It never degrades
into an empty profile: "could not ask" and "nothing is wrong" are the two answers
this project exists to keep apart.

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
