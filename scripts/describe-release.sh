#!/bin/sh
# Prints the release identity of this build context: the tag the commit sits
# under, or the nearest tag plus distance, never a guess.
#
# Order of preference:
#   1. BUILD_RELEASE from the environment, when a pipeline already worked it out.
#   2. `git describe --tags --always --dirty`, whose output only claims a tag
#      when the tag is actually reachable from HEAD.
#   3. The short commit sha, when the history carries no tags at all.
#   4. "unknown", when there is no repository to ask.
#
# A value that cannot be proven is worse than no value, so nothing here
# fabricates a plausible-looking version.
set -eu

if [ -n "${BUILD_RELEASE:-}" ]; then
	printf '%s\n' "$BUILD_RELEASE"
	exit 0
fi

# `git describe` walks up from the current directory, so confirm this really is
# the repository root before trusting anything it says.
if ! git rev-parse --git-dir >/dev/null 2>&1; then
	printf 'unknown\n'
	exit 0
fi

# A shallow clone holds the commit but not the history behind it, so describe
# degrades to a bare sha. Try to deepen once. Failure is expected and ignored:
# a build context often has no credentials to fetch with, and the fallbacks
# below still produce something honest.
git_dir=$(git rev-parse --git-dir)
if [ -f "$git_dir/shallow" ]; then
	git fetch --unshallow --tags --quiet >/dev/null 2>&1 ||
		git fetch --tags --quiet >/dev/null 2>&1 || true
fi

if described=$(git describe --tags --always --dirty 2>/dev/null) && [ -n "$described" ]; then
	printf '%s\n' "$described"
	exit 0
fi

# Abbreviated to the same 7 characters `git describe` uses, so a sha-only
# release is as long as the sha inside a described one.
if sha=$(git rev-parse --short=7 HEAD 2>/dev/null) && [ -n "$sha" ]; then
	printf '%s\n' "$sha"
	exit 0
fi

printf 'unknown\n'
