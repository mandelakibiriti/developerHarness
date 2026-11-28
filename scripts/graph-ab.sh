#!/usr/bin/env bash
# A/B/C test of code-graph search for agent codebase questions.
#
#   A  baseline: Claude Code with Read/Grep/Glob only
#   B  A + the harness MCP server, graph.search in keyword (BM25) mode
#   C  A + the harness MCP server, graph.search default (semantic: embeddings from
#      harness.json "embeddings" if set, else the agent's LLM provider)
#
# Each question is asked in every arm with headless `claude -p`. An answer
# covers a requirement when it names any function in that requirement's group
# (questions with just file+symbol also need the file). Tokens, cost and turns
# come from Claude Code's own JSON result.
#
# Usage: [MODEL=sonnet] [QUERIES=evals/queries.json] [ARMS="A B C"] scripts/graph-ab.sh [query-id ...]
# C needs working embeddings; the script runs `harness graph embed` first and stops if there are none.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
MODEL=${MODEL:-sonnet}
QUERIES=${QUERIES:-$ROOT/evals/queries.json}
ARMS=${ARMS:-A B C}
OUT="$ROOT/.harness/graph-ab/$(date +%Y%m%dT%H%M%S)-$MODEL-$(basename "$QUERIES" .json).jsonl"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$(dirname "$OUT")" "$ROOT/.harness/bin"
go build -o "$ROOT/.harness/bin/harness" "$ROOT/cmd/harness"

# One harness.json per arm; the absolute root keeps it pointed at this repo.
jq --arg root "$ROOT" '.project.root = $root | del(.embeddings)' "$ROOT/harness.json" >"$TMP/lexical.json"
jq --arg root "$ROOT" '.project.root = $root' "$ROOT/harness.json" >"$TMP/semantic.json"
if [[ " $ARMS " == *" C "* ]]; then
  "$ROOT/.harness/bin/harness" graph embed --config "$TMP/semantic.json"
fi
mcp() { jq -nc --arg bin "$ROOT/.harness/bin/harness" --arg cfg "$1" '{mcpServers:{harness:{command:$bin,args:["mcp","--config",$cfg]}}}'; }

HINT='For questions about this codebase, first call the harness execute tool with graph.search; for multi-part questions search each part, in one execute call, and follow calls with graph.neighbors. Each hit has file:line and doc (its doc comment and declaration). Answer from those when they settle the question; otherwise Read only the returned spans, never whole files.'

ask() { # arm question
  local arm=$1 q=$2 cfg='{"mcpServers":{}}' tools="Read Grep Glob" extra=()
  case $arm in
    B) cfg=$(mcp "$TMP/lexical.json") ;;
    C) cfg=$(mcp "$TMP/semantic.json") ;;
  esac
  if [ "$arm" != A ]; then
    tools="$tools mcp__harness__execute mcp__harness__search mcp__harness__describe"
    extra=(--append-system-prompt "$HINT")
  fi
  (cd "$ROOT" && claude -p "Answer from this repository: $q
Name each function as \`name\` (file:line), then explain in at most three sentences." \
    --output-format json --model "$MODEL" --no-session-persistence \
    --setting-sources project --strict-mcp-config --mcp-config "$cfg" \
    --tools Read Grep Glob --allowedTools $tools --max-budget-usd 1.5 \
    ${extra[@]+"${extra[@]}"} </dev/null 2>/dev/null) || echo '{"is_error":true,"result":"claude exited non-zero"}'
}

jq -c '.queries[]' "$QUERIES" | while read -r row; do
  id=$(jq -r .id <<<"$row")
  if [ $# -gt 0 ] && [[ " $* " != *" $id "* ]]; then continue; fi
  q=$(jq -r .q <<<"$row")
  for arm in $ARMS; do
    res=$(ask "$arm" "$q")
    jq -c --arg id "$id" --arg arm "$arm" --argjson row "$row" '
      (.result // "") as $a
      | ($row.require // [[$row.symbol]]) as $req
      | ([$req[] | select(any(.[]; . as $n | $a | test("\\b" + $n + "\\b")))] | length) as $got
      | ($row.file // "") as $file
      | {
          id: $id, arm: $arm,
          covered: $got, required: ($req | length),
          correct: ($got == ($req | length) and ($file == "" or ($a | contains($file)))),
          input_tokens: ((.usage.input_tokens // 0) + (.usage.cache_creation_input_tokens // 0) + (.usage.cache_read_input_tokens // 0)),
          output_tokens: (.usage.output_tokens // 0),
          cost_usd: (.total_cost_usd // 0), turns: (.num_turns // 0), ms: (.duration_ms // 0),
          error: (.is_error // false), answer: ($a | gsub("\n"; " ") | .[0:240])
        }' <<<"$res" | tee -a "$OUT"
  done
done

echo "--- summary ($OUT)"
jq -s 'group_by(.arm) | map({arm: .[0].arm, runs: length,
  complete: (map(select(.correct)) | length),
  coverage_pct: ((map(.covered) | add) * 1000 / (map(.required) | add) | round / 10),
  input_tokens: (map(.input_tokens) | add), output_tokens: (map(.output_tokens) | add),
  cost_usd: ((map(.cost_usd) | add) * 10000 | round / 10000),
  turns: (map(.turns) | add), secs: ((map(.ms) | add) / 1000 | round), errors: (map(select(.error)) | length)})' "$OUT"
