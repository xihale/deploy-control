#!/usr/bin/env bash
# deployd script for poetry.xeed.ink (site: poetry). Runs as poetry-ci with
# cwd = deployd's checked-out worktree (exactly DEPLOY_COMMIT).
#
# Build is pure (corpus is committed): bun install → corpus-check → generate.
# Publish: rsync into /srv/poetry; /_nuxt/ /corpus/ /fonts/ never delete, they
# retire into ~/poetry-attic (immutable-cached bundles / corpus stamps / fonts
# referenced by cached CSS). Attic pruned by its own age.
set -Eeuo pipefail
umask 022

SITE=poetry
BRANCH="${DEPLOY_BRANCH:?DEPLOY_BRANCH missing}"
SHA="${DEPLOY_COMMIT:?DEPLOY_COMMIT missing}"
DEST=/srv/poetry
source /var/lib/deployd/scripts/notify.sh

say() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die() { say "FATAL: $*"; dc_notify failure "$SITE deploy failed: $*"; exit 1; }
on_err() { local rc=$?; dc_notify failure "$SITE deploy failed (exit $rc)"; }
trap on_err ERR

say "=== deploy start ($BRANCH, sha=$SHA, job=$DEPLOY_JOB_ID) ==="

bun install --frozen-lockfile
bun run corpus-check
bun run generate

# --- publish ----------------------------------------------------------------
ATTIC="$HOME/poetry-attic"
mkdir -p "$ATTIC"
rsync -a --delete --delay-updates --exclude=/_nuxt --exclude=/corpus --exclude=/fonts \
  --exclude=/deploy-status.json \
  --chmod=Du=rwx,Dgo=rx,Fu=rw,Fgo=r \
  .output/public/ "$DEST/"
# --checksum: generate rewrites .output every time, so mtimes are always fresh
# and quick-check would re-copy (and thus --backup) all 64 MB of corpus every
# deploy; content comparison retires only genuinely replaced files.
for tree in _nuxt corpus fonts; do
  rsync -a --delete --checksum --backup --backup-dir="$ATTIC/$(date -u +%Y%m%dT%H%M%S)" \
    --chmod=Du=rwx,Dgo=rx,Fu=rw,Fgo=r \
    ".output/public/$tree/" "$DEST/$tree/"
done
find "$ATTIC" -mindepth 1 -maxdepth 1 -type d -mtime +6 -exec rm -rf {} +

# Meta after the last rsync: certifies the completed publish.
printf '{"sha":"%s","branch":"%s","deployedAt":"%s"}\n' \
  "$SHA" "$BRANCH" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$DEST/deploy-meta.json"

say "published ${SHA:0:7} → $DEST (attic: $(du -sh "$ATTIC" 2>/dev/null | cut -f1))"
dc_notify success "${SHA:0:7}"
say "=== deploy done ==="
