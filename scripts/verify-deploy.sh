#!/bin/sh
# Verifies that velm.dev is actually running a given commit.
#
# Why this exists (#191): Railway skips a build whenever the CI check suite on
# the commit is not green — a CodeQL job failing on disk space is enough — and
# still reports the deployment to GitHub as a success. Nothing in the deploy
# path disagreed, so production sat on the previous commit while dashboards
# showed a healthy release. This script is the disagreement: it asks the
# running site what it serves and fails when the answer is another commit.
#
# Usage: verify-deploy.sh <expected-commit> [site-url]
#
# The commit is compared as a prefix (>= 7 chars) because Railway and
# /version disagree on short versus full SHAs for the same commit.
# "unknown" never matches: a build with no identity cannot be verified.
set -eu

expected="${1:-}"
site="${2:-https://velm.dev}"
url="${VELM_VERSION_URL:-$site/version}"
attempts="${VERIFY_ATTEMPTS:-18}"   # 18 * 20s = 6 minutes
interval="${VERIFY_INTERVAL:-20}"

if [ -z "$expected" ]; then
	echo "usage: $0 <expected-commit> [site-url]" >&2
	exit 2
fi

expected="$(printf '%s' "$expected" | tr 'A-F' 'a-f')"
served=""

attempt=1
while [ "$attempt" -le "$attempts" ]; do
	body="$(curl -fsS --max-time 10 "$url" 2>/dev/null || true)"
	served="$(printf '%s' "$body" | sed -n 's/.*"commit":[[:space:]]*"\([^"]*\)".*/\1/p' | tr 'A-F' 'a-f')"
	[ -n "$served" ] || served="unreachable"

	if [ "$served" != "unknown" ] && [ "$served" != "unreachable" ] && [ -n "$served" ]; then
		# Prefix match in either direction, minimum 7 characters.
		if [ "$served" = "$expected" ]; then
			echo "deploy verified: $url serves $served"
			exit 0
		fi
		if [ "${#served}" -ge 7 ] && [ "${expected#"$served"}" != "$expected" ]; then
			echo "deploy verified: $url serves $served (short form of $expected)"
			exit 0
		fi
		if [ "${#expected}" -ge 7 ] && [ "${served#"$expected"}" != "$served" ]; then
			echo "deploy verified: $url serves $served"
			exit 0
		fi
	fi

	if [ "$attempt" -lt "$attempts" ]; then
		sleep "$interval"
	fi
	attempt=$((attempt + 1))
done

echo "deploy NOT verified: $url serves $served, expected $expected" >&2
echo "The merge to main did not reach production. Railway reports the deployment as a success even when it skips the build (CI check suite not green), so nothing else will flag this." >&2
exit 1
