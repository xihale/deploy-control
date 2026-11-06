#!/usr/bin/env bash
# deployd script for zp.xeed.ink (site: zig-playground). Runs as zig-ci with
# cwd = deployd's checked-out worktree (exactly DEPLOY_COMMIT).
#
#   1. fill public/compilers/<id>/ from the public `compilers` release
#      (incremental: ids with meta.json are never re-downloaded; untracked
#      cache survives deployd's checkout -f, so it stays incremental)
#   2. fallback: build missing ids from source via build-compilers.mjs
#      (hostZig lives in ~/.local/share/zvm, bootstrapped on demand)
#   3. npm ci + vite build + assemble dist
#   4. pre-gzip wasm, rsync to /srv/zig-playground; hashed /assets/ retire
#      into ~/zp-attic for ~7d (must outlive the 5d shell TTL in Caddy)
#
# github.com release downloads retry direct first, then through the fetch
# proxy deployd hands us (DEPLOY_FETCH_PROXY) — mirrors the old blog script's
# direct-then-snirect pattern.
set -Eeuo pipefail
umask 022

SITE=zig-playground
BRANCH="${DEPLOY_BRANCH:?DEPLOY_BRANCH missing}"
SHA="${DEPLOY_COMMIT:?DEPLOY_COMMIT missing}"
DEST=/srv/zig-playground
RELEASE_TAG="${COMPILERS_RELEASE:-compilers}"
REPO_URL="https://github.com/xihale/zig-playground"
source /var/lib/deployd/scripts/notify.sh

say() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die() { say "FATAL: $*"; dc_notify failure "$SITE deploy failed: $*"; exit 1; }
on_err() { local rc=$?; dc_notify failure "$SITE deploy failed (exit $rc)"; }
trap on_err ERR

# curl with one direct attempt then one proxied attempt (if a proxy is set).
try_curl() { # <url> <outfile>
  local url="$1" out="$2"
  curl -fsSL --retry 2 -o "$out" "$url" && return 0
  if [ -n "${DEPLOY_FETCH_PROXY:-}" ]; then
    HTTPS_PROXY="$DEPLOY_FETCH_PROXY" HTTP_PROXY="$DEPLOY_FETCH_PROXY" \
      curl -fsSL --retry 2 -o "$out" "$url" && return 0
  fi
  return 1
}

say "=== deploy start ($BRANCH, sha=$SHA, job=$DEPLOY_JOB_ID) ==="

# --- compilers: incremental fill from the public release -------------------
mkdir -p public/compilers .zig-version-cache
mapfile -t IDS < <(node --input-type=module -e '
  import { loadVersionsManifest } from "./scripts/versions-lib.mjs";
  process.stdout.write(loadVersionsManifest().versions.map(v => v.id).join("\n"));
')
missing=()
for id in "${IDS[@]}"; do
  if [ -f "public/compilers/$id/meta.json" ]; then
    say "compilers/$id: cached"
    continue
  fi
  if try_curl "$REPO_URL/releases/download/$RELEASE_TAG/$id.tar.gz" \
      ".zig-version-cache/$id.tar.gz"; then
    mkdir -p "public/compilers/$id"
    tar -xzf ".zig-version-cache/$id.tar.gz" -C "public/compilers/$id"
    say "compilers/$id: fetched from release $RELEASE_TAG"
  else
    missing+=("$id")
  fi
done

# --- fallback: build missing ids from source (as the old CI workflow did) ---
if [ "${#missing[@]}" -gt 0 ]; then
  say "not in release: ${missing[*]} — rebuilding from source"
  mapfile -t HOSTS < <(node --input-type=module -e '
    import { loadVersionsManifest } from "./scripts/versions-lib.mjs";
    const hosts = [...new Set(loadVersionsManifest().versions.map(v => v.hostZig).filter(Boolean))];
    process.stdout.write(hosts.join("\n"));
  ')
  mkdir -p "$HOME/.local/share/zvm"
  index=$(mktemp)
  try_curl https://ziglang.org/download/index.json "$index" \
    || die "could not download ziglang.org index.json"
  for host in "${HOSTS[@]}"; do
    dest="$HOME/.local/share/zvm/$host"
    [ -x "$dest/zig" ] && continue
    if [ "$host" = "master" ]; then
      url=$(jq -r '.master["x86_64-linux"].tarball' "$index")
    else
      url=$(jq -r --arg v "$host" '.[$v]["x86_64-linux"].tarball // empty' "$index")
    fi
    if [ -z "$url" ] || [ "$url" = "null" ]; then
      die "no x86_64-linux tarball for hostZig=$host"
    fi
    tmp=$(mktemp -d)
    try_curl "$url" "$tmp/zig.tar.xz" || die "could not download zig $host"
    tar -xJ -C "$tmp" -f "$tmp/zig.tar.xz"
    inner=$(find "$tmp" -maxdepth 1 -type d -name 'zig-*' | head -1)
    mkdir -p "$dest"
    cp -a "$inner"/. "$dest"/
    "$dest/zig" version
  done
  node scripts/build-compilers.mjs --select all --skip-existing
fi

node --input-type=module -e '
  import { loadVersionsManifest } from "./scripts/versions-lib.mjs";
  import { existsSync } from "node:fs";
  const m = loadVersionsManifest();
  const miss = m.versions.filter(v => !existsSync(`public/compilers/${v.id}/meta.json`));
  if (miss.length) { console.error("missing compiler packages:", miss.map(v => v.id).join(" ")); process.exit(1); }
  if (!existsSync(`public/compilers/${m.default}/meta.json`)) { console.error("default compiler missing:", m.default); process.exit(1); }
'

# --- frontend build + publish ----------------------------------------------
npm ci
npm run build
find dist -name "*.wasm" -exec gzip -k9 {} +

# --- publish ----------------------------------------------------------------
ATTIC="$HOME/zp-attic"
mkdir -p "$ATTIC"
rsync -a --delete --delay-updates --exclude=/assets \
  --chmod=Du=rwx,Dgo=rx,Fu=rw,Fgo=r \
  dist/ "$DEST/"
rsync -a --delete --checksum --backup --backup-dir="$ATTIC/$(date -u +%Y%m%dT%H%M%S)" \
  --chmod=Du=rwx,Dgo=rx,Fu=rw,Fgo=r \
  dist/assets/ "$DEST/assets/"
find "$ATTIC" -mindepth 1 -maxdepth 1 -type d -mtime +6 -exec rm -rf {} +

printf '{"sha":"%s","branch":"%s","deployedAt":"%s"}\n' \
  "$SHA" "$BRANCH" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$DEST/deploy-meta.json"

say "published ${SHA:0:7} → $DEST (attic: $(du -sh "$ATTIC" 2>/dev/null | cut -f1))"
dc_notify success "$SITE deployed ${SHA:0:7} → zp.xeed.ink"
say "=== deploy done ==="
