#!/bin/sh
# Rehearse every command the README shows, against the fixtures in this
# repository, with the exit code treated as the assertion.
#
# Why a script and not a reading pass: a README's commands are claims, and the
# claim is almost never "it printed something that looked right". It is "a clean
# profile exits 0", "a blocked one exits 1", "a typo exits 2 and is not mistaken
# for a verdict". Those are exactly the facts a person skimming the document
# cannot check, and exactly the facts that drift when a flag is renamed.
#
# It dials nothing. The registry section is rehearsed through `--offline` and a
# seeded cache directory whose `--registry-url` points at a closed port: if any
# step ever opened a socket the output would say "could not be checked" instead
# of the cached answer, and the step would fail.
#
#   $ sh scripts/readme-rehearsal.sh
#   $ DSHFIT_BIN=/path/to/dshfit sh scripts/readme-rehearsal.sh
#
# Exit: 0 every claim held, 1 a claim did not, 2 the rehearsal could not run.
set -u

repo=$(cd "$(dirname "$0")/.." && pwd)
cd "$repo" || exit 2

bin=${DSHFIT_BIN:-}
if [ -z "$bin" ]; then
	if [ -x ./bin/dshfit ]; then
		bin=./bin/dshfit
	else
		echo "readme-rehearsal: building the binary first (make build)" >&2
		make build >/dev/null 2>&1 || {
			echo "readme-rehearsal: make build failed" >&2
			exit 2
		}
		bin=./bin/dshfit
	fi
fi
case $bin in
/*) ;;
*) bin="$repo/${bin#./}" ;;
esac
if [ ! -x "$bin" ]; then
	echo "readme-rehearsal: no runnable binary at $bin" >&2
	exit 2
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/dshfit-rehearsal.XXXXXX") || exit 2
trap 'rm -rf "$work"' EXIT INT TERM

total=0
failed=0
out=$work/out.txt
err=$work/err.txt

# run executes the binary and records the exit code and both streams.
run() {
	total=$((total + 1))
	"$bin" "$@" >"$out" 2>"$err"
	code=$?
}

# run_in is the same with stdin taken from a file: `profile -` reads the listing
# from a pipe, and a rehearsal that cannot feed a pipe cannot check that path.
run_in() {
	src=$1
	shift
	total=$((total + 1))
	"$bin" "$@" <"$src" >"$out" 2>"$err"
	code=$?
}

# shellcheck disable=SC2317 # called from the assertion helpers below
fail() {
	failed=$((failed + 1))
	echo "FAIL [$total] $step: $*" >&2
	echo "  command: $cmd" >&2
	sed 's/^/  out: /' "$out" | head -20 >&2
	sed 's/^/  err: /' "$err" | head -10 >&2
}

step() {
	step=$1
	cmd=$2
	echo "— $step"
}

expect_code() {
	[ "$code" -eq "$1" ] || fail "exit code $code, expected $1"
}

expect_text() {
	grep -qF -- "$1" "$out" || fail "output does not contain \"$1\""
}

expect_no_text() {
	if grep -qF -- "$1" "$out"; then fail "output must not contain \"$1\""; fi
}

# Diagnostics belong on stderr — the protocol and the reports own stdout — so a
# claim about a diagnostic has to read the stream it is actually on.
expect_err() {
	grep -qF -- "$1" "$err" || fail "stderr does not contain \"$1\""
}

# ---------------------------------------------------------------------------
# Identity: the version has to be a build identity, not the word "dev".
# ---------------------------------------------------------------------------
step "dshfit version" "version"
run version
expect_code 0
expect_text "dshfit "
expect_no_text "unknown, unknown"

step "dshfit --version" "--version"
run --version
expect_code 0
expect_text "dshfit "

step "dshfit help" "help"
run help
expect_code 0
expect_text "usage:"
expect_text "dshfit profile --host"

step "no arguments is a usage error" "(no args)"
run
expect_code 2
expect_err "usage:"

step "an unknown command is not a verdict" "nope"
run nope
expect_code 2

# ---------------------------------------------------------------------------
# check: one plugin at a time, the first example in the README.
# ---------------------------------------------------------------------------
step "check: a plugin that loads exits 0" "check --host 0.2.2-rc.1 testdata/profile/web/plugins/dsh-context/"
run check --host 0.2.2-rc.1 testdata/profile/web/plugins/dsh-context/
expect_code 0
expect_text "OK"
expect_text "1 plugin(s) against host 0.2.2-rc.1: 1 ok, 0 warn, 0 blocked"

step "check: a 0.1.x caret against a 0.2 host is blocked" "check --host 0.2.2-rc.1 …/dsh-narrow-range/"
run check --host 0.2.2-rc.1 testdata/profile/web/plugins/dsh-narrow-range/
expect_code 1
expect_text "BLOCKED"
expect_text "peer-range-excludes-host"
expect_text "will not load"

step "check --json carries verdict and issues" "check --host 0.2.2-rc.1 --json …"
run check --host 0.2.2-rc.1 --json testdata/profile/web/plugins/dsh-narrow-range/
expect_code 1
expect_text '"verdict": "blocked"'
expect_text '"code": "peer-range-excludes-host"'
expect_text '"source": "dir"'

# The tarball paragraph: what only an archive can prove. The archive is built
# here rather than committed, so the rehearsal cannot drift from npm's layout
# without someone noticing the tar entries below.
pkg=$work/tar/package
mkdir -p "$pkg/lib"
cat >"$pkg/package.json" <<'JSON'
{"name":"dsh-plugin-tgz","version":"1.0.0","files":["lib","docs","cordis.patch.yml"],
 "peerDependencies":{"@deepseek-ai/dsh-app-boot":"^0.2.0-rc.2"},
 "dsh":{"bundle":{"patch":"./cordis.patch.yml"}}}
JSON
printf -- '- insert:\n    - id: dsh-plugin-tgz\n      name: dsh-plugin-tgz\n' >"$pkg/cordis.patch.yml"
printf 'export default 1\n' >"$pkg/lib/index.js"
( cd "$work/tar" && tar -czf "$work/broken.tgz" package ) || {
	echo "readme-rehearsal: tar failed" >&2
	exit 2
}

step "check a .tgz: files[] names a path npm did not pack" "check --host 0.2.0-rc.2 broken.tgz"
run check --host 0.2.0-rc.2 "$work/broken.tgz"
expect_code 1
expect_text "declared-file-not-in-tarball"
expect_text "(tarball, files[] cross-checked against the archive)"

# Reverse assertion: an archive that contains everything it declares must not be
# blocked. A rehearsal that only proves the failure mode would pass while the
# tool blocked every tarball.
okpkg=$work/tar-ok/package
mkdir -p "$okpkg/lib"
cat >"$okpkg/package.json" <<'JSON'
{"name":"dsh-plugin-tgz","version":"1.0.0","files":["lib","cordis.patch.yml"],
 "peerDependencies":{"@deepseek-ai/dsh-app-boot":"^0.2.0-rc.2"},
 "dsh":{"bundle":{"patch":"./cordis.patch.yml"}}}
JSON
printf -- '- insert:\n    - id: dsh-plugin-tgz\n      name: dsh-plugin-tgz\n' >"$okpkg/cordis.patch.yml"
printf 'export default 1\n' >"$okpkg/lib/index.js"
( cd "$work/tar-ok" && tar -czf "$work/ok.tgz" package ) || exit 2
step "check a clean .tgz exits 0" "check --host 0.2.0-rc.2 ok.tgz"
run check --host 0.2.0-rc.2 "$work/ok.tgz"
expect_code 0
expect_text "OK"

# ---------------------------------------------------------------------------
# profile: three formats, one verdict, and the exit code unchanged by format.
# ---------------------------------------------------------------------------
step "profile: text groups by root cause" "profile --host 0.2.2-rc.1 web/list.json"
run profile --host 0.2.2-rc.1 testdata/profile/web/list.json
expect_code 1
expect_text "blocked by root cause"
expect_text "smallest fix:"
expect_text "5 package(s): 1 ok, 0 warn, 2 blocked, 1 unknown, 1 not a plugin"

step "profile: a clean profile exits 0" "profile --host 0.2.2-rc.1 clean/list.json"
run profile --host 0.2.2-rc.1 testdata/profile/clean/list.json
expect_code 0
expect_text "no findings"
expect_text "not a plugin"

step "profile reads a listing from stdin" "profile … - < web/list.json"
run_in testdata/profile/web/list.json profile --host 0.2.2-rc.1 -
expect_code 1
expect_text "web against host 0.2.2-rc.1"

step "profile --json is machine-readable" "profile --host 0.2.2-rc.1 --json"
run profile --host 0.2.2-rc.1 --json testdata/profile/web/list.json
expect_code 1
expect_text '"counts"'
expect_text '"groups"'
expect_text '"status"'

step "profile --format markdown keeps the exit code and grows no registry" "profile --format markdown"
run profile --host 0.2.2-rc.1 --format markdown testdata/profile/web/list.json
expect_code 1
expect_text "### dshfit · host"
expect_text "| cause | range | plugins | rejects | smallest fix |"
expect_no_text "registry — releases the publisher ships"

step "profile --json and --format markdown together exit 2" "profile --json --format markdown"
run profile --host 0.2.2-rc.1 --json --format markdown testdata/profile/web/list.json
expect_code 2
expect_err "ask for different formats"

# ---------------------------------------------------------------------------
# Registry: the cached, offline path. --registry-url points at a closed port so
# a step that dialled would report a failure instead of an answer.
# ---------------------------------------------------------------------------
deadurl=http://127.0.0.1:9
cache=$work/registry-cache
mkdir -p "$cache"
pkgname=dsh-pet
host=0.2.2-rc.1
# The cache file name is a hash of package + host + registry URL. Recomputing it
# here is the point: if that key ever stops covering the URL, this seeded file
# stops matching and the step fails rather than quietly serving the wrong answer.
if command -v sha256sum >/dev/null 2>&1; then
	hashcmd=sha256sum
else
	hashcmd="shasum -a 256"
fi
suffix=$(printf '%s\0%s\0%s' "$pkgname" "$host" "$deadurl" | $hashcmd | cut -c1-8)
cat >"$cache/$pkgname@$host-$suffix.json" <<JSON
{
  "package": "$pkgname",
  "host": "$host",
  "registry": "$deadurl",
  "fetched_at": "2026-11-12T12:39:53Z",
  "packument": {
    "name": "$pkgname",
    "dist_latest": "0.4.8",
    "versions": [{"version": "0.4.8", "peers": {"@deepseek-ai/dsh-app-boot": "^0.2.0-rc.1"}}]
  }
}
JSON

step "profile --offline: a cache hit is labelled with its age" "profile --offline --cache-dir …"
run profile --host 0.2.2-rc.1 --offline --cache-dir "$cache" --registry-url "$deadurl" testdata/profile/web/list.json
expect_code 1
expect_text "registry — releases the publisher ships"
expect_text "dsh-pet  latest 0.4.8 does not admit 0.2.2-rc.1"
expect_text "(from cache, offline)"
expect_text "data as of "

step "profile --offline: a cache miss is reported, not skipped" "profile --offline (empty cache)"
run profile --host 0.2.2-rc.1 --offline --cache-dir "$work/empty-cache" --registry-url "$deadurl" testdata/profile/web/list.json
expect_code 1
expect_text "could not be checked"
expect_text "no cached registry data"
expect_no_text "(from cache"

step "a plain profile run never grows a registry section" "profile (no --registry)"
run profile --host 0.2.2-rc.1 --registry-url "$deadurl" testdata/profile/web/list.json
expect_code 1
expect_no_text "releases the publisher ships"

# ---------------------------------------------------------------------------
# diff: what changes between two hosts.
# ---------------------------------------------------------------------------
step "diff: blocked → blocked with a new cause is a difference" "diff --from 0.2.2-rc.1 --to 0.3.0"
run diff --from 0.2.2-rc.1 --to 0.3.0 testdata/profile/web/list.json
expect_code 1
expect_text "root cause changed"
expect_text "blocked → blocked"
expect_text "peer-range-excludes-host(prerelease-gate)"
expect_text "peer-range-excludes-host(out-of-range)"

step "diff: nothing changes exits 0 even when things are blocked" "diff --from 0.2.1-rc.3 --to 0.2.2-rc.1"
run diff --from 0.2.1-rc.3 --to 0.2.2-rc.1 testdata/profile/web/list.json
expect_code 0
expect_text "no differences"
expect_text "this says the upgrade changes nothing, not that the profile is healthy"

step "diff without both hosts exits 2" "diff --from 0.2.2-rc.1"
run diff --from 0.2.2-rc.1 testdata/profile/web/list.json
expect_code 2

# ---------------------------------------------------------------------------
# serve: the line protocol.
# ---------------------------------------------------------------------------
step "serve answers one request with one line" "serve <<< version"
printf '{"id":1,"method":"version"}\n' >"$work/req.txt"
run_in "$work/req.txt" serve
expect_code 0
expect_text '"protocol":1'
expect_text '"name":"dshfit"'

step "serve answers an unknown method with a code, not an empty result" "serve unknown-method"
printf '{"id":2,"method":"teleport"}\n' >"$work/req2.txt"
run_in "$work/req2.txt" serve
expect_code 0
expect_text '"code":"unknown-method"'

step "serve refuses a bare - as the listing" "serve listing=-"
printf '{"id":3,"method":"profile","params":{"host":"0.2.2-rc.1","listing":"-"}}\n' >"$work/req3.txt"
run_in "$work/req3.txt" serve
expect_code 0
expect_text '"error"'

# ---------------------------------------------------------------------------
# pre-commit: the three exit codes, and the escape hatch it names.
# ---------------------------------------------------------------------------
step "pre-commit without DSHFIT_HOST exits 2" "sh scripts/pre-commit.sh"
total=$((total + 1))
DSHFIT_BIN="$bin" DSHFIT_LISTING="$work/missing.json" sh scripts/pre-commit.sh >"$out" 2>"$err"
code=$?
expect_code 2
expect_err "set DSHFIT_HOST"

step "pre-commit on a blocked profile exits 1 and names --no-verify" "DSHFIT_HOST=0.3.0 sh scripts/pre-commit.sh"
total=$((total + 1))
DSHFIT_BIN="$bin" DSHFIT_HOST=0.3.0 DSHFIT_LISTING=testdata/profile/web/list.json \
	sh scripts/pre-commit.sh >"$out" 2>"$err"
code=$?
expect_code 1
expect_err "--no-verify"

# ---------------------------------------------------------------------------
# Not rehearsed here, on purpose:
#   make ci / make fuzz — `ci` is the test job itself, and `fuzz` runs by chance
#   for a fixed time; running either from inside a test would be a rehearsal of
#   the rehearsal. `make build` was already used above to produce the binary.
# ---------------------------------------------------------------------------
echo ""
if [ "$failed" -ne 0 ]; then
	echo "readme-rehearsal: $failed of $total claim(s) did not hold"
	exit 1
fi
echo "readme-rehearsal: $total claim(s) held"
