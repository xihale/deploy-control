#!/usr/bin/env bash
# deployd script for xeed.ink (site: blog). Runs as blog-ci with cwd set to
# deployd's checked-out worktree (exactly DEPLOY_COMMIT).
#
# Fetch/checkout/locking are deployd's job now; this script only builds and
# publishes. Gates kept identical to the old repo script (formerly started by
# adnanh/webhook): bun install --frozen-lockfile → autocorrect lint →
# astro check → astro build, then rsync into /var/www/xeed.ink with the same
# /_astro attic policy (browser-cached HTML may reference retired chunks).
set -Eeuo pipefail
umask 022

SITE=blog
BRANCH="${DEPLOY_BRANCH:?DEPLOY_BRANCH missing}"
SHA="${DEPLOY_COMMIT:?DEPLOY_COMMIT missing}"
DEST=/var/www/xeed.ink
source /var/lib/deployd/scripts/notify.sh

say() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die() { say "FATAL: $*"; dc_notify failure "$SITE deploy failed: $*"; exit 1; }
on_err() { local rc=$?; dc_notify failure "$SITE deploy failed (exit $rc)"; }
trap on_err ERR

say "=== deploy start ($BRANCH, sha=$SHA, job=$DEPLOY_JOB_ID) ==="

# `bun run lint` (autocorrect --fix) mutates tracked files; deployd's next
# `git checkout -f` wipes them — same cleanup role the old script's
# `git reset --hard` played. Untracked caches (node_modules) survive.
bun install --frozen-lockfile
bun run lint
bunx astro check
bun run build

# --- publish ----------------------------------------------------------------
ATTIC="$HOME/blog-attic"
mkdir -p "$ATTIC"
rsync -a --delete --delay-updates --exclude=/_astro --exclude=/deploy-status.json \
  --chmod=Du=rwx,Dgo=rx,Fu=rw,Fgo=r \
  dist/ "$DEST/"
# --checksum: dist mtimes are always fresh, so quick-check would rewrite (and
# thus --backup) every file each deploy; content comparison makes the attic
# hold only genuinely retired assets.
rsync -a --delete --checksum --backup --backup-dir="$ATTIC/$(date -u +%Y%m%dT%H%M%S)" \
  --chmod=Du=rwx,Dgo=rx,Fu=rw,Fgo=r \
  dist/_astro/ "$DEST/_astro/"
# GNU find rounds age up: +6 = 7 full days; prune from retirement time.
find "$ATTIC" -mindepth 1 -maxdepth 1 -type d -mtime +6 -exec rm -rf {} +

# Meta written after the last rsync so it certifies the completed publish
# (the old script shipped it inside dist/ before rsync ran).
printf '{"sha":"%s","branch":"%s","deployedAt":"%s"}\n' \
  "$SHA" "$BRANCH" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$DEST/deploy-meta.json"

say "published ${SHA:0:7} → $DEST (attic: $(du -sh "$ATTIC" 2>/dev/null | cut -f1))"
dc_notify success "$SITE deployed ${SHA:0:7} → xeed.ink"
say "=== deploy done ==="
