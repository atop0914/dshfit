# Fuzz corpus

`go test -fuzz` writes a failing input to `testdata/fuzz/<Target>/` next to the
package that owns the target. This directory is where those files land:

```
internal/semver/testdata/fuzz/FuzzParseRange/<hash>
internal/semver/testdata/fuzz/FuzzExplain/<hash>
internal/registry/testdata/fuzz/FuzzRegistryParse/<hash>
internal/registry/testdata/fuzz/FuzzCacheRecord/<hash>
```

A file here is not a curiosity — it is a regression test. `go test` replays every
entry in the corpus on an ordinary run, so once an input is committed the crash
it found cannot come back without the suite going red. That is why anything the
fuzzer writes belongs in the same commit as the fix, and why `make fuzz` is kept
out of `make ci`: a fuzz run finds inputs by chance, while the corpus is
deterministic evidence.

The directory exists (rather than appearing the first time a run fails) so that
a document can refer to it and so the convention is discoverable before someone
needs it.
