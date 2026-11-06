#!/usr/bin/env bash
# Shared deployd-script notification helper. Sourced by each site script;
# reads the invoking CI user's ~/.ntfy-notify (line 1: topic, line 2: user:token).
# Never fails a deploy: missing creds or curl errors are swallowed.
# Callers must set $SITE before calling. Mirrors the old webhook-deploy runner:
# success → priority default + ✅, failure → priority high + 🚨.
dc_notify() {
	local status="$1"; shift
	[ -f "$HOME/.ntfy-notify" ] || return 0
	local topic auth pri emoji
	topic=$(head -n1 "$HOME/.ntfy-notify" 2>/dev/null) || return 0
	auth=$(sed -n '2p' "$HOME/.ntfy-notify" 2>/dev/null)
	[ -n "$topic" ] || return 0
	if [ "$status" = success ]; then
		pri=default; emoji=white_check_mark
	else
		pri=high; emoji=rotating_light
	fi
	curl -fsS -m 5 -u "$auth" \
		-H "Priority: $pri" -H "Title: $emoji $*" \
		--data-binary "$SITE deploy $status" \
		"https://ntfy.xeed.ink/$topic" >/dev/null 2>&1 || true
}
