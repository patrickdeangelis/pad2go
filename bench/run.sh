#!/usr/bin/env bash
# Run both benchmark harnesses 3 times, the startup probe, and write RESULTS.md.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
src="${1:-/tmp/s2c-orig/src}"
mkdir -p "$here/results"
bin="$(mktemp -d)/bench"
(cd "$here/.." && go build -o "$bin" ./bench/go)
for i in 1 2 3; do
  "$bin" 2>/dev/null > "$here/results/go_$i.json"
  (cd "$here/python" && python3 bench.py "$src" 2>/dev/null) > "$here/results/python_$i.json"
done
"$here/startup.sh" "$src" > "$here/results/startup.txt"
python3 "$here/compare.py"
