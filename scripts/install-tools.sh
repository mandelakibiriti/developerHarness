#!/usr/bin/env bash
# Installs the DevEx Harness tools locally, idempotently. Safe to re-run.
#
#   HARNESS_TOOLS defaults to ~/.harness/tools. The six tools are installed
#   upstream and run unmodified (never forked), so upgrades stay cheap.
set -euo pipefail

TOOLS_DIR="${HARNESS_TOOLS:-$HOME/.harness/tools}"
mkdir -p "$TOOLS_DIR"

echo "==> agent-trail (npm global)"
if command -v agent-trail >/dev/null 2>&1; then
  echo "    already on PATH"
else
  npm install -g @camtrik/agent-trail
fi

clone() {
  local name="$1" url="$2"
  if [ -d "$TOOLS_DIR/$name/.git" ]; then
    echo "    $name already cloned (cd $TOOLS_DIR/$name && git pull to update)"
  else
    echo "    cloning $name -> $TOOLS_DIR/$name"
    git clone --depth 1 "$url" "$TOOLS_DIR/$name"
  fi
}

echo "==> TokenTelemetry (Spend)"
clone tokentelemetry https://github.com/VasiHemanth/tokentelemetry

echo "==> ARTEX (Secure)"
clone ARTEX https://github.com/BaaRai/ARTEX

echo "==> Floorplan (Map)"
clone floorplan https://github.com/haydenschmitty/floorplan

echo "==> LearnHarness (Learn, desktop app)"
clone learnharness https://github.com/amosblomqvist/learnharness

echo "==> verify"
if command -v plannotator >/dev/null 2>&1; then
  echo "    plannotator: ok ($(plannotator --version 2>&1 | head -1))"
else
  echo "    WARN: plannotator not on PATH (https://github.com/backnotprop/plannotator)"
fi
if [ -f "$TOOLS_DIR/floorplan/serve.mjs" ]; then
  echo "    floorplan: ok ($TOOLS_DIR/floorplan/serve.mjs)"
else
  echo "    WARN: floorplan serve.mjs missing"
fi
command -v graphify >/dev/null 2>&1 && echo "    graphify: ok" || echo "    WARN: graphify not on PATH (pip install graphifyy)"
command -v node    >/dev/null 2>&1 && echo "    node: ok"    || echo "    WARN: node not on PATH"
command -v python3 >/dev/null 2>&1 && echo "    python3: ok" || echo "    WARN: python3 not on PATH"

echo
echo "Done. Next: go run ./cmd/harness doctor && go run ./cmd/harness up"
