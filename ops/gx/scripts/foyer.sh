#!/usr/bin/env bash
# deployd script for home.xeed.ink (site: foyer). Runs as foyer-ci (login
# shell /bin/sh — this script's bash shebang is what matters) with cwd =
# deployd's checked-out worktree (exactly DEPLOY_COMMIT).
#
#   1. bootstrap Go toolchain into ~/.local/share/go (gx has no system go;
#      go.dev flaky-to-blocked from this VPS, China mirrors are reliable)
#   2. bun install + qwik build → frontend/dist (go:embed eats it)
#   3. go build → ~/.local/bin/foyer
#   4. systemctl --user restart foyer; Caddy proxies home.xeed.ink → :3002
#
# Data (db + fetched icons) lives at ~/.local/state/foyer and is never touched.
set -Eeuo pipefail
umask 022

SITE=foyer
BRANCH="${DEPLOY_BRANCH:?DEPLOY_BRANCH missing}"
SHA="${DEPLOY_COMMIT:?DEPLOY_COMMIT missing}"
BIN="$HOME/.local/bin/foyer"
GO_VERSION=1.26.5 # >= go.mod's go directive; bump both together
GO_ROOT="$HOME/.local/share/go"
source /var/lib/deployd/scripts/notify.sh

say() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die() { say "FATAL: $*"; dc_notify failure "$SITE deploy failed: $*"; exit 1; }
on_err() { local rc=$?; dc_notify failure "$SITE deploy failed (exit $rc)"; }
trap on_err ERR

say "=== deploy start ($BRANCH, sha=$SHA, job=$DEPLOY_JOB_ID) ==="

# --- Go toolchain (keep it user-owned like zvm does) ------------------------
if [ ! -x "$GO_ROOT/go/bin/go" ]; then
  say "bootstrapping go $GO_VERSION into $GO_ROOT"
  mkdir -p "$GO_ROOT"
  # go.dev is flaky-to-blocked from gx; the China mirrors are the reliable path.
  for host in https://go.dev/dl https://golang.google.cn/dl \
    https://mirrors.aliyun.com/golang; do
    if curl -fsSL --retry 2 -o "$GO_ROOT/go.tgz" \
      "$host/go$GO_VERSION.linux-amd64.tar.gz"; then
      rm -rf "$GO_ROOT/go"
      tar -xz -C "$GO_ROOT" -f "$GO_ROOT/go.tgz"
      break
    fi
  done
  [ -x "$GO_ROOT/go/bin/go" ] || die "go bootstrap failed from all mirrors"
fi
GO="$GO_ROOT/go/bin/go"
export GOTOOLCHAIN=local # a bumped go.mod must fail loudly, not download
# proxy.golang.org and go.dev are blocked from this VPS; goproxy.cn answers in
# ~50ms. Override by exporting GOPROXY before invoking deployd if that changes.
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

# --- user bus: deployd spawns us without a login session --------------------
export XDG_RUNTIME_DIR="/run/user/$(id -u)"
export DBUS_SESSION_BUS_ADDRESS="unix:path=$XDG_RUNTIME_DIR/bus"

# --- build ------------------------------------------------------------------
(cd frontend && bun install --frozen-lockfile && bun run build)
say "building with $("$GO" version)"
"$GO" build -trimpath -ldflags='-s -w' -o "$BIN.new" .

# --- install + restart (previous binary kept for one-shot rollback) ---------
[ -f "$BIN" ] && cp "$BIN" "$BIN.prev"
mv "$BIN.new" "$BIN"
mkdir -p "$HOME/.config/systemd/user"
cat scripts/server/foyer.service > "$HOME/.config/systemd/user/foyer.service"
systemctl --user daemon-reload
systemctl --user enable --now foyer
systemctl --user restart foyer
up=0
for _ in $(seq 1 30); do
  if curl -fsS -m 2 -o /dev/null http://127.0.0.1:3002/; then up=1; break; fi
  sleep 0.5
done
if [ "$up" != 1 ]; then
  if [ -f "$BIN.prev" ]; then
    say "service did not come up — rolling back to previous binary"
    cp "$BIN.prev" "$BIN"
    systemctl --user restart foyer
  fi
  die "foyer not healthy on 127.0.0.1:3002 after restart"
fi

say "published ${SHA:0:7} → $BIN (foyer active, :3002 healthy)"
dc_notify success "$SITE deployed ${SHA:0:7} (home.xeed.ink healthy)"
say "=== deploy done ==="
