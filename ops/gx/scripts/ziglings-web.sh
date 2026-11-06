#!/usr/bin/env bash
# deployd script for zlg.xeed.ink (site: ziglings-web). Runs as ziglings-ci
# with cwd = deployd's checked-out worktree (exactly DEPLOY_COMMIT).
#
# Pure consumer of zp compilers (loaded at runtime via zp-loader.js). Deploy =
# CI gate (catalog integrity, smoke, unit tests, version alignment — a failing
# check keeps the old site live) → npm ci + vite build → rsync --delete to
# /srv/ziglings-web. The vendor/ziglings-src submodule is NOT needed: builds
# and checks use the committed vendor/ziglings tree.
set -Eeuo pipefail
umask 022

SITE=ziglings-web
BRANCH="${DEPLOY_BRANCH:?DEPLOY_BRANCH missing}"
SHA="${DEPLOY_COMMIT:?DEPLOY_COMMIT missing}"
DEST=/srv/ziglings-web
source /var/lib/deployd/scripts/notify.sh

say() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die() { say "FATAL: $*"; dc_notify failure "$SITE deploy failed: $*"; exit 1; }
on_err() { local rc=$?; dc_notify failure "$SITE deploy failed (exit $rc)"; }
trap on_err ERR

say "=== deploy start ($BRANCH, sha=$SHA, job=$DEPLOY_JOB_ID) ==="

# --- CI gate (same checks as .github/workflows/ci.yml) ----------------------
npm ci
npm run check-catalog
npm run smoke-verify
npm test
npm run check-version-alignment

# --- frontend build + publish -----------------------------------------------
npm run build

# Single replace-all rsync, same as the old script (dist/ contains meta? No:
# meta is written after, so --delete cannot race it).
rsync -a --delete --delay-updates \
  --chmod=Du=rwx,Dgo=rx,Fu=rw,Fgo=r \
  dist/ "$DEST/"
printf '{"sha":"%s","branch":"%s","deployedAt":"%s"}\n' \
  "$SHA" "$BRANCH" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$DEST/deploy-meta.json"

say "published ${SHA:0:7} → $DEST"
dc_notify success "${SHA:0:7}"
say "=== deploy done ==="
