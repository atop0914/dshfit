#!/bin/sh
# Stop a commit when the profile in this repository will not load on the harness
# version you are about to upgrade to.
#
# Nothing here installs a hook. Writing into .git/hooks is a change to your
# repository that you did not ask this script to make, and a hook that silently
# blocks commits is the fastest way to get a tool uninstalled. Wiring it up is
# one line, taken deliberately:
#
#   $ dsh plugin --profile web list --json > .dshfit-listing.json   # once
#   $ git config core.hooksPath .githooks                           # optional
#   $ ln -s ../../scripts/pre-commit.sh .githooks/pre-commit
#
# Exit codes are the tool's, unchanged: 0 nothing will break, 1 something will
# not load or could not be judged, 2 the command itself is misconfigured. A
# pre-commit hook that reported 2 as "your commit is bad" would send people
# looking for a plugin problem that does not exist.
set -u

host=${DSHFIT_HOST:-}
listing=${DSHFIT_LISTING:-.dshfit-listing.json}

if [ -z "$host" ]; then
	echo "pre-commit: set DSHFIT_HOST to the harness version you are upgrading to" >&2
	echo "            DSHFIT_HOST=0.3.0 sh scripts/pre-commit.sh" >&2
	exit 2
fi

bin=${DSHFIT_BIN:-}
if [ -z "$bin" ]; then
	for candidate in ./bin/dshfit dshfit; do
		if [ -x "$candidate" ] || command -v "$candidate" >/dev/null 2>&1; then
			bin=$candidate
			break
		fi
	done
fi
if [ -z "$bin" ]; then
	echo "pre-commit: no dshfit binary found" >&2
	echo "            go install github.com/atop0914/dshfit/cmd/dshfit@latest" >&2
	exit 2
fi

if [ ! -f "$listing" ]; then
	echo "pre-commit: no listing at $listing" >&2
	echo "            dsh plugin --profile <name> list --json > $listing" >&2
	exit 2
fi

"$bin" profile --host "$host" "$listing"
code=$?

case $code in
0)
	exit 0
	;;
1)
	echo "" >&2
	echo "pre-commit: this profile will not load on $host as it stands." >&2
	echo "            The report above names each cause and the smallest edit that" >&2
	echo "            fixes it. If you are committing a range change on purpose," >&2
	echo "            say so in the message and re-run with --no-verify." >&2
	exit 1
	;;
*)
	echo "pre-commit: dshfit exited $code — the command failed, no verdict was reached" >&2
	exit "$code"
	;;
esac
