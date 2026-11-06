#!/usr/bin/env bash
# deployd script for sched.xeed.ink (site: schedmate). Runs as sched-ci with
# cwd = deployd's checked-out worktree (exactly DEPLOY_COMMIT).
#
# bun install + vite build, then rsync to /srv/sched. Hashed /assets/ retire
# into ~/sched-attic for ~7d (must outlive the 5d shell TTL set in Caddy).
set -Eeuo pipefail
umask 022

SITE=schedmate
BRANCH="${DEPLOY_BRANCH:?DEPLOY_BRANCH missing}"
SHA="${DEPLOY_COMMIT:?DEPLOY_COMMIT missing}"
DEST=/srv/sched
source /var/lib/deployd/scripts/notify.sh

say() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die() { say "FATAL: $*"; dc_notify failure "$SITE deploy failed: $*"; exit 1; }
on_err() { local rc=$?; dc_notify failure "$SITE deploy failed (exit $rc)"; }
trap on_err ERR

say "=== deploy start ($BRANCH, sha=$SHA, job=$DEPLOY_JOB_ID) ==="

bun install
bun run build

# --- publish ----------------------------------------------------------------
ATTIC="$HOME/sched-attic"
mkdir -p "$ATTIC"
rsync -a --delete --delay-updates --exclude=/assets \
  --chmod=Du=rwx,Dgo=rx,Fu=rw,Fgo=r \
  dist/ "$DEST/"
# --checksum: dist mtimes are always fresh; content comparison retires only
# genuinely replaced assets.
rsync -a --delete --checksum --backup --backup-dir="$ATTIC/$(date -u +%Y%m%dT%H%M%S)" \
  --chmod=Du=rwx,Dgo=rx,Fu=rw,Fgo=r \
  dist/assets/ "$DEST/assets/"
# +6 = 7 full days = 5d shell TTL + 2d margin.
find "$ATTIC" -mindepth 1 -maxdepth 1 -type d -mtime +6 -exec rm -rf {} +

printf '{"sha":"%s","branch":"%s","deployedAt":"%s"}\n' \
  "$SHA" "$BRANCH" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$DEST/deploy-meta.json"

say "published ${SHA:0:7} → $DEST (attic: $(du -sh "$ATTIC" 2>/dev/null | cut -f1))"
dc_notify success "${SHA:0:7}"
say "=== deploy done ==="
