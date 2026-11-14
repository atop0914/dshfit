#!/bin/sh
# The one command a user actually types is `go install ...@latest`, and it runs
# no Makefile: none of the -ldflags this repository's `make build` passes ever
# happens. A previous project learned this after publishing a release: `make`
# was green, the tag was pushed, and the binary a reader installed printed
# `dev (unknown, unknown)` because the version was injected only by the
# Makefile and read by nobody.
#
# So this script installs the module the way a user does, into an empty GOBIN,
# and then runs the README's first example from a directory that contains
# nothing but a plugin. Two claims are checked: the version is a real build
# identity, and the tool works without the repository on disk.
#
#   $ sh scripts/install-rehearsal.sh          # what `@latest` resolves to
#   $ sh scripts/install-rehearsal.sh dev      # a branch
#   $ sh scripts/install-rehearsal.sh <sha>    # an exact commit
#
# It needs the module proxy, so it is deliberately not part of `make ci`: a
# proxy hiccup would go red for a reason that has nothing to do with this
# project's code. Run it before tagging, which is when it matters.
set -u

ref=${1:-latest}
module=github.com/atop0914/dshfit
step() { echo "— $*"; }
die() {
	echo "FAIL: $*" >&2
	exit 1
}

command -v go >/dev/null 2>&1 || die "go is not on PATH"

work=$(mktemp -d "${TMPDIR:-/tmp}/dshfit-install.XXXXXX") || die "cannot create a temp directory"
trap 'rm -rf "$work"' EXIT INT TERM

step "go install $module/cmd/dshfit@$ref"
GOBIN=$work/bin go install "$module/cmd/dshfit@$ref" || die "go install failed (network or an unknown ref?)"

bin=$work/bin/dshfit
[ -x "$bin" ] || bin=$work/bin/dshfit.exe
[ -x "$bin" ] || die "go install produced no binary in $work/bin"

step "the installed binary reports a real build identity"
version=$("$bin" version) || die "\`dshfit version\` exited non-zero"
echo "  $version"
case $version in
*unknown*)
	die "the version string contains a placeholder: $version"
	;;
"dshfit dev")
	die "the installed binary reports the build default: -ldflags were not applied and nothing fell back"
	;;
esac
case $version in
"dshfit "*) ;;
*) die "unexpected version output: $version" ;;
esac
# The module version has to be in there. For a `@latest` that resolves to a
# pseudo-version it is the only identity the build has, and it is exactly what a
# user pastes into a bug report.
case $version in
*[0-9].[0-9]*) ;;
*) die "no version number in $version" ;;
esac

step "the README's first example, in a directory with nothing but a plugin"
plugin=$work/run/plugins/dsh-openviking
mkdir -p "$plugin" || die "cannot create $plugin"
cat >"$plugin/package.json" <<'JSON'
{
  "name": "dsh-openviking",
  "version": "2.0.1",
  "files": ["lib", "cordis.patch.yml"],
  "peerDependencies": {"@deepseek-ai/dsh-app-boot": "^0.1.0-rc.6"},
  "engines": {"dsh": ">=0.1.0-rc.6"},
  "dsh": {"bundle": {"patch": "./cordis.patch.yml"}}
}
JSON
printf -- '- insert:\n    - id: dsh-openviking\n      name: dsh-openviking\n' >"$plugin/cordis.patch.yml"

set +e
out=$(cd "$work/run" && "$bin" check --host 0.2.0-rc.2 ./plugins/dsh-openviking) 
code=$?
set -e
printf '%s\n' "$out" | sed 's/^/  /'
[ "$code" -eq 1 ] || die "check exited $code, the README says 1 when anything is blocked"
case $out in
*BLOCKED*) ;;
*) die "no BLOCKED line: the headline case did not reproduce" ;;
esac
case $out in
*prerelease*) ;;
*) die "the explanation does not mention the prerelease gate" ;;
esac

step "help and the version flag exit 0"
"$bin" help >/dev/null || die "\`dshfit help\` exited non-zero"
"$bin" --version >/dev/null || die "\`dshfit --version\` exited non-zero"

echo ""
echo "install-rehearsal: the user's command works ($ref → $version)"
