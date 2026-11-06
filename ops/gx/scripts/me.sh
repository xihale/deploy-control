#!/usr/bin/env bash
# deployd script for me.xeed.ink (site: me). Runs as me-ci with cwd = deployd's
# checked-out worktree (exactly DEPLOY_COMMIT).
#
# bun install --frozen-lockfile + astro build, then rsync into /srv/me with
# the /_astro attic policy (browser-cached HTML may reference retired chunks).
set -Eeuo pipefail
umask 022

SITE=me
BRANCH="${DEPLOY_BRANCH:?DEPLOY_BRANCH missing}"
SHA="${DEPLOY_COMMIT:?DEPLOY_COMMIT missing}"
DEST=/srv/me
source /var/lib/deployd/scripts/notify.sh

say() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die() { say "FATAL: $*"; dc_notify failure "$SITE deploy failed: $*"; exit 1; }
on_err() { local rc=$?; dc_notify failure "$SITE deploy failed (exit $rc)"; }
trap on_err ERR

say "=== deploy start ($BRANCH, sha=$SHA, job=$DEPLOY_JOB_ID) ==="

bun install --frozen-lockfile
bun run build

# --- publish ----------------------------------------------------------------
ATTIC="$HOME/me-attic"
mkdir -p "$ATTIC"
rsync -a --delete --delay-updates --exclude=/_astro --exclude=/deploy-status.json \
  --chmod=Du=rwx,Dgo=rx,Fu=rw,Fgo=r \
  dist/ "$DEST/"
rsync -a --delete --checksum --backup --backup-dir="$ATTIC/$(date -u +%Y%m%dT%H%M%S)" \
  --chmod=Du=rwx,Dgo=rx,Fu=rw,Fgo=r \
  dist/_astro/ "$DEST/_astro/"
find "$ATTIC" -mindepth 1 -maxdepth 1 -type d -mtime +6 -exec rm -rf {} +

printf '{"sha":"%s","branch":"%s","deployedAt":"%s"}\n' \
  "$SHA" "$BRANCH" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$DEST/deploy-meta.json"

say "published ${SHA:0:7} → $DEST (attic: $(du -sh "$ATTIC" 2>/dev/null | cut -f1))"
dc_notify success "$SITE deployed ${SHA:0:7} → me.xeed.ink"
say "=== deploy done ==="
