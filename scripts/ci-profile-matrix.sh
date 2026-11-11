#!/bin/sh
# Run dshfit over the fixture matrix, write a markdown summary, and fail when the
# verdict contradicts the table below.
#
# Two jobs in one script, on purpose:
#   1. render what a reviewer reads in the PR (step summary, not a PR comment)
#   2. fail when a verdict changes
# Without 2 the job is decoration: a step that only prints is a step nobody
# reads twice. Without 1 the failure is a red cross with no explanation.
#
# On the summary target: this writes to $GITHUB_STEP_SUMMARY, never a PR
# comment. On a pull request from a fork the GITHUB_TOKEN is read-only, so
# posting a comment needs write access the fork workflow does not have and
# returns 403 — a job turning red on a permission error looks like the tool is
# broken, and the first thing a maintainer does with a broken-looking gate is
# switch it off. The step summary needs no token at all.
#
# Exit codes are the tool's own and are not redefined here:
#   0 = nothing blocked and nothing unread
#   1 = a plugin will not load, or a package could not be judged
#   2 = usage or configuration error
# 1 is an *expected* outcome for these fixtures — they exist to produce one. The
# job fails on 2, on any other unexpected code, or on a count that disagrees with
# the table, because those are the only outcomes that mean "something changed".
set -u

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd) || exit 1
cd "$root" || exit 1

bin=${DSHFIT_BIN:-bin/dshfit}
if [ ! -x "$bin" ]; then
	echo "no $bin — run 'make build' first" >&2
	exit 2
fi
if ! command -v jq >/dev/null 2>&1; then
	# Falling back to grep would make the count assertions silently weaker,
	# which is exactly the failure mode this gate exists to prevent.
	echo "jq is required: the gate reads counts from --json" >&2
	exit 2
fi

summary=${GITHUB_STEP_SUMMARY:-}
emit() {
	if [ -n "$summary" ]; then
		printf '%s\n' "$1" >>"$summary"
	fi
	printf '%s\n' "$1"
}

cases=$(mktemp) || exit 1
trap 'rm -f "$cases"' EXIT INT TERM

# fixture host expected_exit blocked unknown ok
#
# These are measured facts about the fixtures, not wishes. When a change is meant
# to move one of them, update the row in the same commit — that edit is the
# reviewable proof that somebody thought about it.
cat >"$cases" <<'CASES'
clean 0.3.0 0 0 0 1
drift 0.2.2-rc.1 1 2 0 1
drift 0.3.0 1 3 0 0
web 0.2.2-rc.1 1 2 1 1
web 0.3.0 1 2 1 1
CASES

emit "## dshfit · fixture matrix"
emit ""
emit "Each case below is a real listing judged against a real harness version."
emit "\`1\` means plugins will not load — for these fixtures that is the expected"
emit "answer, not a failure. A red job means a count moved or the tool itself"
emit "errored (\`2\`), never merely that something is blocked."
emit ""

failures=0
tmpout=$(mktemp) || exit 1
tmperr=$(mktemp) || exit 1
trap 'rm -f "$cases" "$tmpout" "$tmperr"' EXIT INT TERM

while read -r fixture host want_exit want_blocked want_unknown want_ok; do
	case $fixture in '' | '#'*) continue ;; esac
	listing="testdata/profile/$fixture/list.json"

	"$bin" profile --host "$host" --json "$listing" >"$tmpout" 2>"$tmperr"
	code=$?

	if [ "$code" -eq 2 ]; then
		# 2 is the tool refusing to answer, not an answer. Letting it share the
		# blocked path would make "you misconfigured the job" indistinguishable
		# from "a plugin will not load".
		echo "::error title=dshfit usage error::${fixture} @ ${host} exited 2: $(tr '\n' ' ' <"$tmperr")"
		failures=$((failures + 1))
		emit "### ${fixture} @ \`${host}\` — **tool error (exit 2)**"
		emit ""
		emit '```'
		cat "$tmperr"
		emit '```'
		emit ""
		continue
	fi
	if [ "$code" -ne 0 ] && [ "$code" -ne 1 ]; then
		echo "::error title=dshfit crashed::${fixture} @ ${host} exited ${code}"
		failures=$((failures + 1))
		continue
	fi

	if ! jq -e '.counts' "$tmpout" >/dev/null 2>&1; then
		echo "::error title=unreadable json::${fixture} @ ${host}: --json did not print a result"
		failures=$((failures + 1))
		continue
	fi
	blocked=$(jq -r '.counts.blocked' "$tmpout")
	unknown=$(jq -r '.counts.unknown' "$tmpout")
	ok=$(jq -r '.counts.ok' "$tmpout")
	plugins=$(jq -r '.counts.plugins' "$tmpout")

	emit "### ${fixture} @ \`${host}\`"
	emit ""
	emit "$("$bin" profile --host "$host" --format markdown "$listing" 2>/dev/null)"
	emit ""

	if [ "$code" -ne "$want_exit" ] || [ "$blocked" -ne "$want_blocked" ] ||
		[ "$unknown" -ne "$want_unknown" ] || [ "$ok" -ne "$want_ok" ]; then
		echo "::error title=verdict changed::${fixture} @ ${host}: exit=${code} blocked=${blocked} unknown=${unknown} ok=${ok} plugins=${plugins}; table says exit=${want_exit} blocked=${want_blocked} unknown=${want_unknown} ok=${want_ok}"
		emit "> **Verdict changed.** got exit=\`${code}\` blocked=\`${blocked}\` unknown=\`${unknown}\` ok=\`${ok}\`; the table in \`scripts/ci-profile-matrix.sh\` expects exit=\`${want_exit}\` blocked=\`${want_blocked}\` unknown=\`${want_unknown}\` ok=\`${want_ok}\`."
		emit ""
		failures=$((failures + 1))
	fi
done <"$cases"

if [ "$failures" -ne 0 ]; then
	emit "**${failures} case(s) contradict the table.** If the change was intended, update the table in \`scripts/ci-profile-matrix.sh\`; if it was not, a range or a gate just moved."
	echo "${failures} case(s) contradict the table" >&2
	exit 1
fi

emit "**All cases match the table.**"
