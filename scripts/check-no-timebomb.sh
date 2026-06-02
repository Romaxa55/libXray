#!/usr/bin/env bash
# check-no-timebomb.sh — guard against date-based "removed feature" time-bombs
# in the local xray-core fork that silently break already-shipped clients.
#
# Context: upstream xray-core gated `allowInsecure` behind a hard fatal after
# 2026-06-01 (transport_internet.go). On that date every MegaV client whose
# config carried allowInsecure:true stopped building its xray config — xray
# went `stopped`, SOCKS/HTTP inbound never came up, "Connection refused".
# We need allowInsecure (CF Workers fronts present mismatched certs, real cert
# verification is impossible), so the fork degrades the fatal to a warning.
#
# This script fails the build if a date-gated fatal sneaks back in — e.g. after
# a clean `go get` resets ../xray-core to upstream and the replace re-points at
# a pristine tree. Run it from build.py right after the -replace is set.

set -euo pipefail

# Resolve fork dir: arg1, or ../xray-core relative to libXray/ (this script lives
# in libXray/scripts/).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FORK_DIR="${1:-$SCRIPT_DIR/../../xray-core}"

if [ ! -d "$FORK_DIR" ]; then
  echo "[timebomb-guard] ERROR: xray-core fork not found at: $FORK_DIR" >&2
  echo "[timebomb-guard] Clone it (Romaxa55/Xray-core@feature/dialer-proxy-balancer) and re-apply patches." >&2
  exit 1
fi

TLS_FILE="$FORK_DIR/infra/conf/transport_internet.go"
if [ ! -f "$TLS_FILE" ]; then
  echo "[timebomb-guard] ERROR: $TLS_FILE missing — fork layout unexpected." >&2
  exit 1
fi

fail=0

# 1) The specific allowInsecure date-bomb: a fatal under a time.Date(...) check.
#    Detect the upstream pattern where AllowInsecure is rejected after a date.
if grep -nE 'time\.Now\(\)\.After\(time\.Date' "$TLS_FILE" >/dev/null 2>&1; then
  echo "[timebomb-guard] ERROR: date-gated branch present in $TLS_FILE" >&2
  echo "[timebomb-guard] The allowInsecure time-bomb (2026-06-01 fatal) appears to have returned." >&2
  echo "[timebomb-guard] Expected: warning-only degrade, config.AllowInsecure = true." >&2
  grep -nE 'time\.Now\(\)\.After\(time\.Date' "$TLS_FILE" >&2 || true
  fail=1
fi

# 2) Belt-and-suspenders: allowInsecure must NOT lead to PrintRemovedFeatureError.
#    Grab the AllowInsecure block and ensure it doesn't reject.
if awk '/if c\.AllowInsecure \{/{f=1} f{print} /^\t\}/{if(f)exit}' "$TLS_FILE" \
     | grep -q 'PrintRemovedFeatureError'; then
  echo "[timebomb-guard] ERROR: allowInsecure block rejects via PrintRemovedFeatureError." >&2
  echo "[timebomb-guard] allowInsecure must stay enabled (degrade to warning), not be removed." >&2
  fail=1
fi

# 3) Confirm the intended degrade is actually in place (positive assertion).
if ! grep -q 'config.AllowInsecure = true' "$TLS_FILE"; then
  echo "[timebomb-guard] ERROR: 'config.AllowInsecure = true' not found — degrade patch missing." >&2
  fail=1
fi

# 4) hysteria OmitMaxDatagramFrameSize date-flip (upstream flips true after
#    2026-09-01, silently changing QUIC datagram behaviour). Must be pinned.
HYST_FILE="$FORK_DIR/transport/internet/hysteria/dialer.go"
if [ -f "$HYST_FILE" ]; then
  if grep -nE 'OmitMaxDatagramFrameSize:\s*time\.Now\(\)\.After\(time\.Date' "$HYST_FILE" >/dev/null 2>&1; then
    echo "[timebomb-guard] ERROR: hysteria OmitMaxDatagramFrameSize date-flip present in $HYST_FILE" >&2
    echo "[timebomb-guard] Pin it to a constant (false) — do not gate behaviour on a date." >&2
    fail=1
  fi
fi

# 5) Catch-all: any remaining behaviour-changing date gate anywhere in the fork.
#    (browser.go version generators are intentional and excluded.)
STRAY=$(grep -rnE 'time\.Now\(\)\.(After|Before)\(time\.Date' "$FORK_DIR" \
          --include="*.go" 2>/dev/null \
          | grep -v '_test.go' \
          | grep -v '/common/utils/browser.go' || true)
if [ -n "$STRAY" ]; then
  echo "[timebomb-guard] ERROR: stray date-gated branch(es) found in fork:" >&2
  echo "$STRAY" >&2
  echo "[timebomb-guard] Defuse each (pin to a constant or degrade) before building." >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo "[timebomb-guard] FAILED — refusing to build a libv2ray with active date-bombs." >&2
  echo "[timebomb-guard] The degrade lives in the fork tree (commit b879ebd1 on" >&2
  echo "[timebomb-guard] feature/dialer-proxy-balancer). Reset ../xray-core back to that" >&2
  echo "[timebomb-guard] branch, or re-apply libXray/patches/0002-defuse-date-timebombs.patch." >&2
  exit 1
fi

echo "[timebomb-guard] OK — allowInsecure degrade + hysteria pin in place, no date-gated fatal/flip in fork."
