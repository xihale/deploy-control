#!/usr/bin/env bash
# deployd script for deploy.xeed.ink (site: deploy-control) — self-bootstrap:
# deployd builds and deploys itself from this repo.
# Runs as root (deployd's service user) with cwd = checked-out worktree.
#   1. corepack pnpm build → web/out
#   2. go build → deployd.new
#   3. rsync web/out → /var/lib/deployd/web/out (live web root)
#   4. binary changed → swap + restart deferred via systemd-run, so this job
#      can still exit 0 and be recorded before the service bounces
# Server notes: go.dev / proxy.golang.org are flaky-to-blocked from this VPS;
# use the China mirrors and goproxy.cn (same pattern as foyer.sh).
set -Eeuo pipefail
umask 022

SITE=deploy-control
SHA="${DEPLOY_COMMIT:?DEPLOY_COMMIT missing}"
WEB_ROOT=/var/lib/deployd/web/out
BIN=/usr/local/bin/deployd
GO_VERSION=1.26.5 # >= go.mod's go directive; bump both together
GO_ROOT=/var/lib/deployd/toolchain/go
source /var/lib/deployd/scripts/notify.sh

say() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die() { say "FATAL: $*"; dc_notify failure "$SITE deploy failed: $*"; exit 1; }
on_err() { local rc=$?; dc_notify failure "$SITE deploy failed (exit $rc)"; }
trap on_err ERR

say "=== deploy start (${DEPLOY_BRANCH:-?}, sha=$SHA, job=$DEPLOY_JOB_ID) ==="

# --- Go toolchain (shared, root-owned; same bootstrap pattern as foyer.sh) ---
if [ ! -x "$GO_ROOT/go/bin/go" ]; then
  say "bootstrapping go $GO_VERSION into $GO_ROOT"
  mkdir -p "$GO_ROOT"
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
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export COREPACK_ENABLE_DOWNLOAD_PROMPT=0

# --- frontend ----------------------------------------------------------------
say "building web (corepack pnpm)"
(cd web && corepack pnpm install --frozen-lockfile && corepack pnpm build)
[ -f web/out/index.html ] || die "web/out/index.html missing after build"

# --- backend -----------------------------------------------------------------
say "building with $("$GO" version)"
"$GO" build -trimpath -o deployd.new ./cmd/deployd

# --- publish -----------------------------------------------------------------
rsync -a --delete web/out/ "$WEB_ROOT"/
say "web published → $WEB_ROOT"

if cmp -s deployd.new "$BIN"; then
  say "binary unchanged; web-only deploy, no restart"
else
  say "binary changed; scheduling deferred swap + restart"
  install -m755 deployd.new "$BIN.next"
  systemd-run --on-active=3 --collect \
    --unit="deployd-self-restart-$DEPLOY_JOB_ID" \
    /bin/sh -c "install -m755 '$BIN.next' '$BIN' && rm -f '$BIN.next' && systemctl restart deployd"
fi

dc_notify success "${SHA:0:7}"
say "=== deploy done ($SHA) ==="
