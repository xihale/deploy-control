#!/usr/bin/env bash
# lib/common.sh — prefabricated deploy helpers for run-site recipes.
#
# A recipe may rely on (all defined here, sourced by run-site before the
# recipe):
#   say / die / on_err          logging + failure reporting (ntfy)
#   dc_notify                   the ntfy notifier
#   publish_tree <out> <dest>   standard static publish (attic policy + meta)
#   write_meta <dest>           deploy-meta.json
#   go_env <ver> [root]         bootstrap Go toolchain, export GO/GOPROXY
#   fetch_file <url> <out>      curl direct-then-proxy (DEPLOY_FETCH_PROXY)
#   lfs_pull / check_lfs_pointers   Git LFS guarantees
#   user_bus_env                XDG/DBUS env for systemctl --user
#
# Recipe contract variables (all optional unless noted):
#   site=NAME          REQUIRED; must match what deployd dispatched
#   dest=PATH          target dir for the default publish_tree
#   out=DIR            build output dir, default dist (out=. for source trees)
#   attic_trees=(...)  immutable-hashed trees retired via --backup attic
#   attic_dir=PATH     attic location, default $HOME/$SITE-attic
#   publish_excludes=(...)  extra rsync --exclude entries for the main pass

say() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }

# dc_notify success "<commit7>" → title "[site] [commit7]", no body (default
# priority). dc_notify failure "reason" → title "🚨 [site]", body reason
# (priority high). Never fails the deploy: creds live in the invoking CI
# user's ~/.ntfy-notify (line 1 topic, line 2 auth); missing creds or errors
# are swallowed.
dc_notify() {
	local status="$1"; shift
	local home="${HOME:-/root}"
	[ -f "$home/.ntfy-notify" ] || return 0
	local topic auth pri title body
	topic=$(head -n1 "$home/.ntfy-notify" 2>/dev/null) || return 0
	auth=$(sed -n '2p' "$home/.ntfy-notify" 2>/dev/null)
	[ -n "$topic" ] || return 0
	if [ "$status" = success ]; then
		pri=default; title="$SITE $*"; body=""
	else
		pri=high; title="🚨 $SITE"; body="$*"
	fi
	curl -fsS -m 5 -u "$auth" \
		-H "Priority: $pri" -H "Title: $title" \
		--data-binary "$body" \
		"https://ntfy.xeed.ink/$topic" >/dev/null 2>&1 || true
}

die() { say "FATAL: $*"; dc_notify failure "$SITE deploy failed: $*"; exit 1; }
on_err() { local rc=$?; dc_notify failure "$SITE deploy failed (exit $rc)"; }

# write_meta <dest> — deploy-meta.json written AFTER the last rsync so it
# certifies the completed publish, not the build.
write_meta() {
	printf '{"sha":"%s","branch":"%s","deployedAt":"%s"}\n' \
		"$SHA" "${BRANCH:-}" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$1/deploy-meta.json"
}

# publish_tree <out> <dest> — the standard static-site publish, parameterized
# by attic_trees / publish_excludes / attic_dir:
#   - main rsync --delete over out/ → dest/, excluding each attic tree and
#     every publish_excludes entry;
#   - each attic tree rsyncs separately with --checksum --backup into
#     $attic_dir/<stamp>: browser-cached HTML may reference retired chunks,
#     so replaced assets retire for ~7 days instead of vanishing. --checksum
#     matters because build tools rewrite mtimes every run; quick-check would
#     re-copy (and thus --backup) everything every deploy. Content comparison
#     retires only genuinely replaced files;
#   - attic stamps older than 7 days are pruned (GNU find rounds up: +6);
#   - deploy-meta.json written last.
# With no attic_trees (stable filenames) it is a single replace-all rsync.
publish_tree() {
	local out="$1" dest="$2"
	local flags=(--chmod=Du=rwx,Dgo=rx,Fu=rw,Fgo=r)
	local tree ex
	for tree in "${attic_trees[@]}"; do flags+=("--exclude=/$tree"); done
	for ex in "${publish_excludes[@]}"; do flags+=("--exclude=$ex"); done

	rsync -a --delete --delay-updates "${flags[@]}" "$out/" "$dest/"
	if [ "${#attic_trees[@]}" -gt 0 ]; then
		mkdir -p "$attic_dir"
		local stamp
		stamp="$(date -u +%Y%m%dT%H%M%S)"
		for tree in "${attic_trees[@]}"; do
			rsync -a --delete --checksum --backup --backup-dir="$attic_dir/$stamp" \
				--chmod=Du=rwx,Dgo=rx,Fu=rw,Fgo=r \
				"$out/$tree/" "$dest/$tree/"
		done
		find "$attic_dir" -mindepth 1 -maxdepth 1 -type d -mtime +6 -exec rm -rf {} +
		write_meta "$dest"
		say "published ${SHA:0:7} → $dest (attic: $(du -sh "$attic_dir" 2>/dev/null | cut -f1))"
	else
		write_meta "$dest"
		say "published ${SHA:0:7} → $dest"
	fi
}

# go_env <version> [root] — bootstrap a Go toolchain unless present, then
# export GO / GOTOOLCHAIN / GOPROXY. go.dev is flaky-to-blocked from gx, so
# downloads try go.dev, golang.google.cn and the Aliyun mirror in turn.
# Default root is $HOME/.local/share/go (user-owned, like zvm); pass an
# explicit root for a shared install owned by the deployd service user.
go_env() {
	local ver="${1:?go_env <version>}" root="${2:-$HOME/.local/share/go}"
	if [ ! -x "$root/go/bin/go" ]; then
		say "bootstrapping go $ver into $root"
		mkdir -p "$root"
		local host
		for host in https://go.dev/dl https://golang.google.cn/dl \
			https://mirrors.aliyun.com/golang; do
			if curl -fsSL --retry 2 -o "$root/go.tgz" \
				"$host/go$ver.linux-amd64.tar.gz"; then
				rm -rf "$root/go"
				tar -xz -C "$root" -f "$root/go.tgz"
				break
			fi
		done
		[ -x "$root/go/bin/go" ] || die "go bootstrap failed from all mirrors"
	fi
	GO="$root/go/bin/go"
	export GO
	export GOTOOLCHAIN=local # a bumped go.mod must fail loudly, not download
	export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
	say "go: $("$GO" version)"
}

# fetch_file <url> <outfile> — curl with one direct attempt then one proxied
# attempt through deployd's sanctioned endpoint (DEPLOY_FETCH_PROXY).
fetch_file() {
	local url="$1" out="$2"
	curl -fsSL --retry 2 -o "$out" "$url" && return 0
	if [ -n "${DEPLOY_FETCH_PROXY:-}" ]; then
		HTTPS_PROXY="$DEPLOY_FETCH_PROXY" HTTP_PROXY="$DEPLOY_FETCH_PROXY" \
			curl -fsSL --retry 2 -o "$out" "$url" && return 0
	fi
	return 1
}

# lfs_pull — deployd's checkout smudges Git LFS via the global required
# filter; this is the belt-and-braces guarantee (and the only path if
# filters were off).
lfs_pull() {
	local attempt ok=""
	for attempt in 1 2 3; do
		if timeout 300 git lfs pull; then ok=1; break; fi
		say "git lfs pull failed — attempt $attempt"
		[ "$attempt" = 3 ] || sleep $((attempt * 5))
	done
	[ -n "$ok" ] || die "git lfs pull failed — keeping last good deploy"
}

# check_lfs_pointers <dir> — a pointer file reaching the web root renders a
# broken image; refuse to publish.
check_lfs_pointers() {
	if grep -rlqs 'version https://git-lfs.github.com/spec/v1' "$1"; then
		die "unsmudged LFS pointers remain under $1/"
	fi
}

# user_bus_env — deployd spawns scripts without a login session; systemctl
# --user needs the XDG runtime dir and the session bus address.
user_bus_env() {
	export XDG_RUNTIME_DIR="/run/user/$(id -u)"
	export DBUS_SESSION_BUS_ADDRESS="unix:path=$XDG_RUNTIME_DIR/bus"
}
