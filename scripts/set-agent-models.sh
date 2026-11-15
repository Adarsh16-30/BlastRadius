#!/usr/bin/env bash
# Pins a model in each ECC agent's frontmatter: heavy agents on heavy models, light on light.
# Edit the table, rerun any time. Requires the main-session model to come from --model, and
# CLAUDE_CODE_SUBAGENT_MODEL NOT set (it would override every agent below).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
declare -A M=(
  [architect]=claude-fable-5-2
  [planner]=claude-opus-5-5
  [security-reviewer]=claude-opus-5-5
  [database-reviewer]=claude-opus-5-5
  [code-reviewer]=claude-sonnet-5-5
  [go-reviewer]=claude-sonnet-5-5
  [tdd-guide]=claude-sonnet-5-5
  [docs-lookup]=claude-sonnet-5-5
  [go-build-resolver]=claude-haiku-5-5
  [build-error-resolver]=claude-haiku-5-5
  [doc-updater]=claude-haiku-5-5
)
for a in "${!M[@]}"; do
  f=.claude/agents/$a.md
  [ -f "$f" ] || { echo "skip (missing): $a"; continue; }
  awk -v m="${M[$a]}" '
    BEGIN { fm=0; done=0 }
    /^---[ \t]*$/ { fm++; if (fm==2 && !done) { print "model: " m; done=1 } print; next }
    fm==1 && /^model:/ { if (!done) { print "model: " m; done=1 } next }
    { print }
    END { if (!done) exit 3 }' "$f" > "$f.tmp" && mv "$f.tmp" "$f" \
    || { rm -f "$f.tmp"; echo "no frontmatter in $f, not changed" >&2; }
done
grep -n '^model:' .claude/agents/*.md
