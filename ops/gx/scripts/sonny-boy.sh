#!/usr/bin/env bash
# deployd script for sonny-boy.xeed.ink (site: sonny-boy). Runs as
# sonny-boy-ci with cwd = deployd's checked-out worktree (exactly
# DEPLOY_COMMIT; LFS smudge already ran during checkout — /etc/gitconfig has
# filter.lfs required=true and git-lfs is in sudo's secure_path).
#
# Static exhibition site: no build. git lfs pull as guarantee, pointer check,
# then rsync into /srv/sonny-boy. Stills/faces/BGM keep stable filenames, so
# the tree replaces wholesale — no attic.
set -Eeuo pipefail
umask 022

SITE=sonny-boy
BRANCH="${DEPLOY_BRANCH:?DEPLOY_BRANCH missing}"
SHA="${DEPLOY_COMMIT:?DEPLOY_COMMIT missing}"
DEST=/srv/sonny-boy
source /var/lib/deployd/scripts/notify.sh

say() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die() { say "FATAL: $*"; dc_notify failure "$SITE deploy failed: $*"; exit 1; }
on_err() { local rc=$?; dc_notify failure "$SITE deploy failed (exit $rc)"; }
trap on_err ERR

say "=== deploy start ($BRANCH, sha=$SHA, job=$DEPLOY_JOB_ID) ==="

# --- media: stills/faces/bgm are Git LFS objects ------------------------------
# deployd's checkout smudges via the global required filter; `git lfs pull`
# is the belt-and-braces guarantee (and the only path if filters were off).
ok=""
for attempt in 1 2 3; do
  if timeout 300 git lfs pull; then ok=1; break; fi
  say "git lfs pull failed — attempt $attempt"
  [ "$attempt" = 3 ] || sleep $((attempt * 5))
done
[ -n "$ok" ] || die "git lfs pull failed — keeping last good deploy"
# A pointer file reaching the web root renders a broken image; refuse to publish.
if grep -rlqs 'version https://git-lfs.github.com/spec/v1' assets; then
  die "unsmudged LFS pointers remain under assets/"
fi

# --- publish ----------------------------------------------------------------
rsync -a --delete \
  --exclude=/.git --exclude=/src --exclude=/scripts \
  --exclude=/README.md --exclude=/.gitattributes --exclude=/.nojekyll \
  --chmod=Du=rwx,Dgo=rx,Fu=rw,Fgo=r \
  ./ "$DEST/"
printf '{"sha":"%s","branch":"%s","deployedAt":"%s"}\n' \
  "$SHA" "$BRANCH" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$DEST/deploy-meta.json"

say "published ${SHA:0:7} → $DEST"
dc_notify success "$SITE deployed ${SHA:0:7} → sonny-boy.xeed.ink"
say "=== deploy done ==="
