#!/usr/bin/env bash
# Process start + code load: wall time and peak RSS over 20 runs each.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
src="${1:-/tmp/s2c-orig/src}"
bin="$(mktemp -d)/switch2connect"
(cd "$here/.." && go build -trimpath -ldflags="-s -w" -o "$bin" ./cmd/switch2connect)
measure() { # label, command...
  local label=$1; shift
  for _ in $(seq 20); do
    /usr/bin/time -l "$@" 2>&1 >/dev/null | awk -v l="$label" '
      /real/ {t=$1} /maximum resident set size/ {r=$1} END {printf "%s %s %s\n", l, t, r}'
  done
}
measure go "$bin" version
measure python python3 -c "
import sys; sys.path.insert(0, '$here/python'); import stubs; stubs.install('$src')
import discoverer, virtual_controller, controller, cemuhook_udp, config"
echo "binary_bytes $(stat -f%z "$bin" 2>/dev/null || stat -c%s "$bin")"
