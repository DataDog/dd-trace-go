#!/bin/sh
set -eu

home=${HOME:-/tmp/agent-home}
gocache=${GOCACHE:-/tmp/go-build}
gomodcache=${GOMODCACHE:-/tmp/go-mod}
export HOME="$home" GOCACHE="$gocache" GOMODCACHE="$gomodcache"

mkdir -p "$home/.codex" "$gocache" "$gomodcache"
if [ "${AGENT_EVAL_AGENT:-}" = "claude" ]; then
	printf '%s\n' '{"projects":{"/workspace":{"hasTrustDialogAccepted":true}}}' > "$home/.claude.json"
fi
if [ -f /run/agent-auth/codex-auth.json ]; then
	cp /run/agent-auth/codex-auth.json "$home/.codex/auth.json"
	chmod 600 "$home/.codex/auth.json"
fi

exec "$@"
