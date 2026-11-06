#!/usr/bin/env bash
# deployd script for lc.xeed.ink (site: light-cathedral). Runs as lc-ci with
# cwd = deployd's checked-out worktree (exactly DEPLOY_COMMIT).
#
# Pure static site: no build. rsync the tree into /srv/light-cathedral. No
# content-hashed assets, so a plain rsync --delete is safe (no attic).
set -Eeuo pipefail
umask 022

SITE=light-cathedral
BRANCH="${DEPLOY_BRANCH:?DEPLOY_BRANCH missing}"
SHA="${DEPLOY_COMMIT:?DEPLOY_COMMIT missing}"
DEST=/srv/light-cathedral
source /var/lib/deployd/scripts/notify.sh

say() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die() { say "FATAL: $*"; dc_notify failure "$SITE deploy failed: $*"; exit 1; }
on_err() { local rc=$?; dc_notify failure "$SITE deploy failed (exit $rc)"; }
trap on_err ERR

say "=== deploy start ($BRANCH, sha=$SHA, job=$DEPLOY_JOB_ID) ==="

# --- publish ----------------------------------------------------------------
mkdir -p "$DEST"
rsync -a --delete --exclude=/.git/ --exclude=/scripts/ --exclude=/README.md \
  --chmod=Du=rwx,Dgo=rx,Fu=rw,Fgo=r \
  ./ "$DEST/"

# Meta after rsync: certifies the completed publish (old script wrote it
# before rsync — inverted order fixed as part of the deployd migration).
printf '{"sha":"%s","branch":"%s","deployedAt":"%s"}\n' \
  "$SHA" "$BRANCH" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$DEST/deploy-meta.json"

say "published ${SHA:0:7} → $DEST"
dc_notify success "$SITE deployed ${SHA:0:7} → lc.xeed.ink"
say "=== deploy done ==="
