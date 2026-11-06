# Where these fixtures come from

## `../pnpm-list-10.34.9.json`

A verbatim transcript, produced on 2026-11-06 by pnpm 10.34.9 on a Windows host
(hence the backslash paths), for a throwaway profile that installed five real
published packages from the public registry:

| package | version |
|---|---|
| `dsh-context` | 0.82.1 |
| `dsh-m` | 0.12.7 |
| `dsh-pet` | 0.4.5 |
| `dsh-plugin-catalog` | 2026.1106.16122 |
| `dsh-whale-widget` | 0.4.2 |

Commands:

```console
$ pnpm add --ignore-scripts dsh-pet dsh-context dsh-whale-widget dsh-m dsh-plugin-catalog
$ pnpm list --json > pnpm-list-10.34.9.json
```

`dsh plugin --profile <name> list --json` is a thin wrapper around the above
(`@deepseek-ai/dsh` 0.2.2-rc.1 passes any argument it does not own straight to
pnpm inside the profile directory), so this is the shape the parser has to
accept, not a shape we invented for it.

The file is kept byte for byte, including the absolute Windows paths in every
`path` field: those paths do not exist on any other machine, and a test that
claims this fixture is parseable must not depend on them existing.

## `web/plugins/*`

Real `package.json` and `cordis.patch.yml` files copied unmodified from the
published tarballs of `dsh-pet@0.4.5`, `dsh-context@0.82.1` and
`dsh-plugin-catalog@2026.1106.16122`. They are metadata only; the licence of each
package is stated in its own `package.json`.

`dsh-narrow-range` is synthetic: a plugin whose peers were written for the 0.1
line, so the fixture has a second, different root cause next to the real one.

`web/list.json` is hand-written in the pnpm shape with relative paths, because
the absolute paths in the real transcript cannot resolve inside a repository.
`dsh-gone` names a directory that does not exist on purpose, and `dsh-context`
appears in both `dependencies` and `devDependencies` on purpose.
