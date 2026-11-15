#!/bin/sh
# A release says which version it is in three places, and the failure mode when
# they disagree is silent: the tag says v1.0.0, the notes describe v1.0.0, and
# the binary a reader installs reports something else. That is not hypothetical
# — a previous project published a release whose binary printed
# `dev (unknown, unknown)` because the version was injected only by the
# Makefile, and `make` was green the whole time.
#
# So this script compares the three, on the tagged commit, before publishing:
#
#   tag        HEAD must be on an annotated release tag (vX.Y.Z)
#   CHANGELOG  its newest section must be that tag, and must have body text
#   binary     `make build`'s own `version` output must name that tag
#
#   $ sh scripts/release-check.sh
#
# Exit: 0 the three agree, 1 they do not, 2 the check itself could not run.
set -u

repo=$(cd "$(dirname "$0")/.." && pwd)
cd "$repo" || exit 2

die() {
	echo "release-check: $*" >&2
	exit 1
}
skip() {
	echo "release-check: $*" >&2
	exit 2
}

command -v git >/dev/null 2>&1 || skip "git is not on PATH"
[ -f CHANGELOG.md ] || skip "no CHANGELOG.md at $repo"

# 1. The tag. `--exact-match` fails unless HEAD *is* the tagged commit, so a tag
# left behind on an older commit is caught here rather than after publication.
tag=$(git describe --tags --exact-match 2>/dev/null) || die "HEAD is not on a tag: a release is identified by its tag, so there is nothing to compare the notes and the binary against"
case $tag in
v[0-9]*.[0-9]*.[0-9]*) ;;
*) die "HEAD is tagged $tag, which is not a release tag (vMAJOR.MINOR.PATCH)" ;;
esac

# A release must be reproducible from its tag. A dirty tree also makes
# `git describe` report `-dirty`, which would put a marker in the version string
# that no published release should carry.
if [ -n "$(git status --porcelain)" ]; then
	git status --short >&2
	die "the working tree is dirty: a published version has to be buildable from its tag alone"
fi

echo "— tag $tag points at HEAD ($(git rev-parse --short HEAD))"

# 2. The changelog. The newest section is the one the tag publishes; taking
# `grep -m1` rather than searching for the tag keeps this honest when the notes
# were written for a different version than the tag that shipped.
top=$(grep -m1 '^## v' CHANGELOG.md) || die "CHANGELOG.md has no '## v...' section"
case $top in
"## $tag"|"## $tag "*) ;;
*) die "CHANGELOG's newest release section is \"$top\", but the tag is $tag" ;;
esac

section=$(awk -v t="## $tag" 'index($0, t) == 1 {f = 1; next} f && /^## / {exit} f' CHANGELOG.md)
lines=$(printf '%s\n' "$section" | grep -c '[^[:space:]]')
[ "$lines" -ge 15 ] || die "the $tag section of CHANGELOG.md has $lines non-blank line(s): release notes that say nothing are not notes"

echo "— CHANGELOG newest section is $tag ($lines non-blank line(s))"

# 3. The binary. `make build` passes -ldflags, so this is the identity a reader
# gets when they download or `go install` a release — not the module fallback.
make build >/dev/null 2>&1 || skip "make build failed"
[ -x ./bin/dshfit ] || skip "make build produced no ./bin/dshfit"

got=$(./bin/dshfit version) || die "\`dshfit version\` exited non-zero"
echo "— binary reports: $got"
case $got in
*unknown*) die "the version string contains a placeholder: $got" ;;
*dirty*) die "the version string carries a dirty marker: $got" ;;
esac
case $got in
"dshfit $tag"|"dshfit $tag "*) ;;
*) die "the built binary reports \"$got\", but the tag is $tag" ;;
esac

echo ""
echo "release-check: tag, CHANGELOG and binary all say $tag"
