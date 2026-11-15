#!/usr/bin/env bash
# Usage: scripts/run-milestones.sh [FROM] [TO]    e.g. scripts/run-milestones.sh M2 M4
# One fresh headless Claude Code session per attempt; state lives in git + docs/, not in context.
# Model per attempt: starting tier from scripts/models.conf, one tier heavier after each failed gate.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
source scripts/models.conf
ALL=(M0 M1 M2 M3 M4 M5 M6 M7)
FROM=${1:-M0}; TO=${2:-M7}; MAX_TRIES=${MAX_TRIES:-3}

pick() {  # pick <milestone> <attempt> -> model id
  local base=${BASE[$1]} i
  for i in "${!LADDER[@]}"; do [ "${LADDER[$i]}" = "$base" ] && break; done
  local n=$(( i + $2 - 1 )); [ "$n" -ge "${#LADDER[@]}" ] && n=$(( ${#LADDER[@]} - 1 ))
  echo "${ID[${LADDER[$n]}]}"
}

mkdir -p .autorun docs/evidence docs/plans; touch docs/PROGRESS.md
grep -qx '.autorun/' .git/info/exclude 2>/dev/null || echo '.autorun/' >> .git/info/exclude
git switch build/auto 2>/dev/null || git switch -c build/auto

# Preflight: every model in the ladder must answer before we start.
for t in "${LADDER[@]}"; do
  claude -p "Reply with the single word OK." --model "${ID[$t]}" --output-format json \
    > ".autorun/preflight-$t.json" 2>&1 \
    || { echo "Preflight failed for ${ID[$t]} (see .autorun/preflight-$t.json)" >&2; exit 1; }
done

run=0
for m in "${ALL[@]}"; do
  [ "$m" = "$FROM" ] && run=1
  [ "$run" = 1 ] || continue
  if git rev-parse -q --verify "refs/tags/done-$m" >/dev/null; then echo "skip $m (tagged)"; continue; fi

  passed=0
  for try in $(seq 1 "$MAX_TRIES"); do
    MODEL=$(pick "$m" "$try")
    echo "== $m attempt $try/$MAX_TRIES on $MODEL =="
    echo "$MODEL" > ".autorun/$m-$try.model"
    PROMPT=$(cat <<PROMPT_EOF
You are running unattended. Milestone $m of docs/PRD.md section 11; attempt $try of $MAX_TRIES.
1. Read CLAUDE.md, docs/DECISIONS.md, docs/PROGRESS.md, and docs/BLOCKED.md if present (a previous attempt failed: fix its cause, then delete the file).
2. Write the file list to docs/plans/$m.md and proceed without waiting.
3. Implement every acceptance criterion for $m, using the skills and agents CLAUDE.md maps to $m.
4. Run the milestone's commands. Paste each command and its real output into docs/evidence/$m.md.
5. If a target is missed, report the measured value and cause; do not loosen it. If you cannot proceed, write docs/BLOCKED.md and stop.
6. Append a handoff to docs/PROGRESS.md, then commit with a message starting "$m:". Leave the working tree clean.
PROMPT_EOF
)
    claude -p "$PROMPT" --model "$MODEL" --permission-mode acceptEdits --max-turns 150 --output-format json \
      > ".autorun/$m-$try.json" 2> ".autorun/$m-$try.err" || echo "claude exited non-zero (see .autorun/$m-$try.err)" >&2
    if scripts/gate.sh "$m" > ".autorun/$m-$try.gate" 2>&1; then passed=1; break; fi
    tail -5 ".autorun/$m-$try.gate"
  done

  [ "$passed" = 1 ] || { echo "STOP: $m failed $MAX_TRIES attempts. See .autorun/ and docs/BLOCKED.md" >&2; exit 1; }
  git tag "done-$m"; echo "== $m PASSED on $MODEL =="
  [ "$m" = "$TO" ] && break
done
echo "Done. Review: git log --oneline --decorate; cat .autorun/*.model; ls .autorun/scorecards-*"
