#!/bin/bash
# Mutation checks for the panel. Each mutant must turn the named assertion red,
# and the tree must be restored afterwards no matter how the run ends.
set -u
cd "C:/Users/yitao.zhu/WorkBuddy/dshfit/plugins/dshfit-panel" || exit 1

run () {
  name="$1"
  if ! npm run build >/dev/null 2>&1; then
    echo "$name -> BUILD-FAIL"
    git checkout -- src
    return
  fi
  out=$(node --test test/render.test.mjs test/client.test.mjs 2>&1)
  red=$(printf '%s' "$out" | grep -c '^not ok')
  echo "$name -> red=$red"
  printf '%s\n' "$out" | grep '^not ok' | head -4
  git checkout -- src
}

# M1: not_a_plugin counted as blocked (the mistake the README table warns about).
sed -i 's/{ label: '"'"'blocked'"'"', value: counts.blocked }/{ label: '"'"'blocked'"'"', value: counts.blocked + counts.not_a_plugin }/' src/render.ts
run "M1 not_a_plugin-counted-as-blocked"

# M2: the summary counts a subset instead of the field it names.
sed -i 's/{ label: '"'"'packages'"'"', value: counts.packages }/{ label: '"'"'packages'"'"', value: counts.blocked }/' src/render.ts
run "M2 summary-counts-a-subset"

# M3: unknown dropped from the tone decision (zero blocked becomes clear).
sed -i 's/if (counts.unknown > 0) {/if (counts.unknown < 0) {/' src/render.ts
run "M3 unknown-dropped-from-tone"

echo "--- restored? ---"
git status --short
# Rebuild after the last mutant: `git checkout -- src` restores the sources but
# leaves lib/ holding whatever the last mutant compiled, and a later `node --test`
# without a build would be testing the mutant.
npm run build >/dev/null 2>&1 && echo "lib rebuilt from restored sources"
