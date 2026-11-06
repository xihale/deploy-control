#!/usr/bin/env bash
# Shared deployd-script notification helper. Sourced by each site script;
# reads the invoking CI user's ~/.ntfy-notify (line 1: topic, line 2: user:token).
# Never fails a deploy: missing creds or curl errors are swallowed.
# Callers must set $SITE before calling. Formats:
#   dc_notify success "${DEPLOY_COMMIT:0:7}"  → title "[site] [commit]", no body
#   dc_notify failure "reason"                → title "🚨 [site]", body reason
dc_notify() {
	local status="$1"; shift
	[ -f "$HOME/.ntfy-notify" ] || return 0
	local topic auth pri title body
	topic=$(head -n1 "$HOME/.ntfy-notify" 2>/dev/null) || return 0
	auth=$(sed -n '2p' "$HOME/.ntfy-notify" 2>/dev/null)
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
